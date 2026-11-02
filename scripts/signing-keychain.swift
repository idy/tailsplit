import CryptoKit
import Darwin
import Foundation
import Security

// Private import data arrives over stdin, never command arguments or a file.
struct SigningIdentity: Decodable {
    let role: String
    let pkcs12: String
    let passphrase: String
}

struct ImportRequest: Decodable {
    let keychainPath: String
    let keychainPassword: String
    let useDefaultKeychain: Bool
    let intermediateCertificate: String
    let identities: [SigningIdentity]
}

func check(_ status: OSStatus, _ operation: String) throws {
    if status != errSecSuccess {
        throw NSError(domain: operation, code: Int(status))
    }
}

do {
    let request = try JSONDecoder().decode(
        ImportRequest.self, from: FileHandle.standardInput.readDataToEndOfFile()
    )
    try check(SecKeychainSetUserInteractionAllowed(false), "Disable import prompts")
    var keychain: SecKeychain?
    let password = Array(request.keychainPassword.utf8)
    if request.useDefaultKeychain {
        try check(SecKeychainCopyDefault(&keychain), "Open default signing keychain")
    } else {
        let created = password.withUnsafeBytes { bytes in
            SecKeychainCreate(
                request.keychainPath, UInt32(bytes.count), bytes.baseAddress, false, nil, &keychain
            )
        }
        try check(created, "Create temporary signing keychain")
    }
    guard let keychain else {
        throw NSError(domain: "Missing signing keychain", code: 1)
    }
    if !request.useDefaultKeychain {
        try check(password.withUnsafeBytes { bytes in
            SecKeychainUnlock(keychain, UInt32(bytes.count), bytes.baseAddress, true)
        }, "Unlock temporary signing keychain")
        var settings = SecKeychainSettings(
            version: UInt32(SEC_KEYCHAIN_SETTINGS_VERS1), lockOnSleep: false,
            useLockInterval: true, lockInterval: 14400
        )
        try check(SecKeychainSetSettings(keychain, &settings), "Set signing keychain lifetime")
        // codesign's private-key lookup also uses the user's search list, even
        // when --keychain limits the certificate lookup. Remove only this
        // temporary entry with SecKeychainDelete after the command finishes.
        var searchList: CFArray?
        try check(SecKeychainCopySearchList(&searchList), "Read signing keychain search list")
        var keychains = (searchList as? [SecKeychain]) ?? []
        keychains.insert(keychain, at: 0)
        try check(SecKeychainSetSearchList(keychains as CFArray), "Register temporary signing keychain")
    }
    guard let intermediateData = Data(base64Encoded: request.intermediateCertificate),
          let intermediate = SecCertificateCreateWithData(nil, intermediateData as CFData)
    else {
        throw NSError(domain: "Invalid Apple intermediate certificate", code: 1)
    }
    let intermediateStatus = SecCertificateAddToKeychain(intermediate, keychain)
    if intermediateStatus != errSecDuplicateItem {
        try check(intermediateStatus, "Import Apple Developer ID intermediate")
    }

    var trustedApplications: [SecTrustedApplication] = []
    for path in [
        "/usr/bin/codesign", "/usr/bin/pkgbuild", "/usr/bin/productbuild",
        "/usr/bin/productsign", CommandLine.arguments[0],
    ] {
        var application: SecTrustedApplication?
        try check(SecTrustedApplicationCreateFromPath(path, &application), "Resolve Apple signing tool")
        if let application {
            trustedApplications.append(application)
        }
    }
    var access: SecAccess?
    try check(SecAccessCreate(
        "TailSplit Developer ID signing" as CFString, trustedApplications as CFArray, &access
    ), "Create signing tool access")
    guard let access else {
        throw NSError(domain: "Missing signing access", code: 1)
    }

    var result: [[String: String]] = []
    for identity in request.identities {
        guard let data = Data(base64Encoded: identity.pkcs12) else {
            throw NSError(domain: "Invalid PKCS12 input", code: 1)
        }
        let options: [String: Any] = [
            kSecImportExportPassphrase as String: identity.passphrase,
            kSecImportExportKeychain as String: keychain,
            kSecImportExportAccess as String: access,
        ]
        var imported: CFArray?
        try check(SecPKCS12Import(data as CFData, options as CFDictionary, &imported), "Import signing identity")
        guard let entries = imported as? [[String: Any]], entries.count == 1,
              let item = entries.first?[kSecImportItemIdentity as String]
        else {
            throw NSError(domain: "Expected one signing identity", code: 1)
        }
        let importedIdentity = item as! SecIdentity
        var certificate: SecCertificate?
        try check(SecIdentityCopyCertificate(importedIdentity, &certificate), "Read imported certificate")
        guard let certificate else {
            throw NSError(domain: "Missing imported certificate", code: 1)
        }
        let der = SecCertificateCopyData(certificate) as Data
        let fingerprint = Insecure.SHA1.hash(data: der).map { String(format: "%02X", $0) }.joined()
        var parameters = SecItemImportExportKeyParameters()
        parameters.version = UInt32(SEC_KEY_IMPORT_EXPORT_PARAMS_VERSION)
        let exportPassphrase: CFTypeRef = identity.passphrase as CFString
        parameters.passphrase = Unmanaged.passUnretained(exportPassphrase)
        var exported: CFData?
        try check(withExtendedLifetime(exportPassphrase) {
            SecItemExport(importedIdentity, .formatPKCS12, [], &parameters, &exported)
        }, "Verify identity export")
        guard let exported else {
            throw NSError(domain: "Missing exported identity", code: 1)
        }
        result.append([
            "role": identity.role, "sha1": fingerprint,
            "exportedPKCS12": (exported as Data).base64EncodedString(),
        ])
    }
    var pathLength: UInt32 = 4096
    var keychainPath = [CChar](repeating: 0, count: Int(pathLength))
    try check(SecKeychainGetPath(keychain, &pathLength, &keychainPath), "Read signing keychain path")
    FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: [
        "keychain": String(cString: keychainPath), "identities": result,
    ]))
} catch let error as NSError {
    // Do not render the request, imported data or password in diagnostics.
    FileHandle.standardError.write(Data("Signing keychain: \(error.domain) (status \(error.code))\n".utf8))
    exit(1)
}
