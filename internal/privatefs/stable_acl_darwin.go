package privatefs

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Darwin extended ACLs can grant access independently of POSIX mode bits. Use
// libSystem's public ACL APIs, including in CGO_ENABLED=0 CLI/helper builds.
// Keep the library loaded while its registered functions can be called.
var darwinACL = sync.OnceValues(func() (darwinACLFunctions, error) {
	var api darwinACLFunctions
	handle, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return api, err
	}
	for _, symbol := range []struct {
		name string
		fn   any
	}{
		{"lstatx_np", &api.statX},
		{"filesec_query_property", &api.secQuery},
		{"filesec_get_property", &api.secGet},
		{"acl_init", &api.init},
		{"acl_get_flagset_np", &api.getFlags},
		{"acl_add_flag_np", &api.addFlag},
		{"acl_set_flagset_np", &api.setFlags},
		{"filesec_init", &api.secInit},
		{"filesec_free", &api.secFree},
		{"filesec_set_property", &api.secSet},
		{"openx_np", &api.openX},
		{"__error", &api.errno},
		{"acl_get_entry", &api.getEntry},
		{"acl_get_tag_type", &api.getTag},
		{"acl_get_permset_mask_np", &api.getMask},
		{"acl_get_qualifier", &api.getQualifier},
		{"acl_free", &api.free},
		{"mbr_uid_to_uuid", &api.uidToUUID},
	} {
		address, err := purego.Dlsym(handle, symbol.name)
		if err != nil {
			_ = purego.Dlclose(handle)
			return darwinACLFunctions{}, err
		}
		purego.RegisterFunc(symbol.fn, address)
	}
	return api, nil
})

type darwinACLFunctions struct {
	statX        func(string, *syscall.Stat_t, unsafe.Pointer) int32
	secQuery     func(unsafe.Pointer, int32, *int32) int32
	secGet       func(unsafe.Pointer, int32, unsafe.Pointer) int32
	init         func(int32) unsafe.Pointer
	getFlags     func(unsafe.Pointer, *unsafe.Pointer) int32
	addFlag      func(unsafe.Pointer, int32) int32
	setFlags     func(unsafe.Pointer, unsafe.Pointer) int32
	secInit      func() unsafe.Pointer
	secFree      func(unsafe.Pointer)
	secSet       func(unsafe.Pointer, int32, unsafe.Pointer) int32
	openX        func(string, int32, unsafe.Pointer) int32
	errno        func() *int32
	getEntry     func(unsafe.Pointer, int32, *unsafe.Pointer) int32
	getTag       func(unsafe.Pointer, *int32) int32
	getMask      func(unsafe.Pointer, *uint64) int32
	getQualifier func(unsafe.Pointer) unsafe.Pointer
	free         func(unsafe.Pointer) int32
	uidToUUID    func(uint32, *[16]byte) int32
}

func checkDirectoryACL(path string, private bool) error {
	return checkPathACL(path, private, true)
}

func checkPrivateFileACL(path string) error {
	return checkPathACL(path, true, false)
}

func createPrivateFile(path string) (*os.File, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return nil, &os.PathError{Op: "open private file", Path: path, Err: syscall.EINVAL}
	}
	api, err := darwinACL()
	if err != nil {
		return nil, err
	}
	acl := api.init(0)
	if acl == nil {
		return nil, errors.New("cannot initialize private file ACL")
	}
	defer api.free(acl)
	var flags unsafe.Pointer
	if api.getFlags(acl, &flags) != 0 || api.addFlag(flags, 1<<17) != 0 || api.setFlags(acl, flags) != 0 { // ACL_FLAG_NO_INHERIT
		return nil, errors.New("cannot disable file ACL inheritance")
	}
	security := api.secInit()
	if security == nil {
		return nil, errors.New("cannot initialize private file security")
	}
	defer api.secFree(security)
	mode := uint16(0o600)                                                                                          // Darwin mode_t
	if api.secSet(security, 4, unsafe.Pointer(&mode)) != 0 || api.secSet(security, 5, unsafe.Pointer(&acl)) != 0 { // FILESEC_MODE, FILESEC_ACL
		return nil, errors.New("cannot prepare private file security")
	}
	// Supplying the ACL at exclusive creation also prevents another user from
	// opening the empty file before a post-open ACL change and retaining access
	// to bytes written later. CLOEXEC avoids leaking it to concurrent helpers.
	for {
		runtime.LockOSThread()
		fd := api.openX(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_CLOEXEC, security)
		var openErr syscall.Errno
		if fd < 0 {
			openErr = syscall.Errno(*api.errno())
		}
		runtime.UnlockOSThread()
		if fd >= 0 {
			return os.NewFile(uintptr(fd), path), nil
		}
		if openErr != syscall.EINTR {
			return nil, &os.PathError{Op: "open private file", Path: path, Err: openErr}
		}
	}
}

func checkPathACL(path string, private, directory bool) error {
	api, err := darwinACL()
	if err != nil {
		return err
	}
	security := api.secInit()
	if security == nil {
		return errors.New("cannot initialize ACL metadata")
	}
	defer api.secFree(security)
	// acl_get_file conflates an absent ACL with errors and frees its native
	// metadata before returning. Inspect property presence directly instead of
	// depending on errno after that cleanup. No stat fields are consumed here;
	// this storage accommodates both exported Darwin inode layouts.
	var stat syscall.Stat_t
	if api.statX(path, &stat, security) != 0 {
		if _, err := os.Lstat(path); err != nil {
			return err
		}
		return errors.New("cannot read ACL metadata")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return errors.New("ACL path must remain the expected file type")
	}
	var present int32
	if api.secQuery(security, 5, &present) != 0 { // FILESEC_ACL
		return errors.New("cannot query ACL metadata")
	}
	if present == 0 {
		return nil
	}
	var acl unsafe.Pointer
	if api.secGet(security, 5, unsafe.Pointer(&acl)) != 0 || acl == nil {
		return errors.New("cannot read storage ACL")
	}
	defer api.free(acl)
	const readOnly = 1<<1 | 1<<3 | 1<<7 | 1<<9 | 1<<11 | 1<<20
	for index := int32(0); index <= 128; index++ { // Darwin ACL_MAX_ENTRIES
		var entry unsafe.Pointer
		// acl_get_file returns a valid kernel ACL. Darwin enumeration returns
		// zero for an entry and -1 at the end, unlike Linux's POSIX ACL API.
		if api.getEntry(acl, index, &entry) != 0 {
			return nil
		}
		if index == 128 {
			return errors.New("storage ACL exceeds the supported entry bound")
		}
		var tag int32
		if api.getTag(entry, &tag) != 0 {
			return errors.New("cannot read storage ACL entry")
		}
		if tag == 2 { // ACL_EXTENDED_DENY, including the usual deny-delete ACL.
			continue
		}
		if tag != 1 { // ACL_EXTENDED_ALLOW
			return errors.New("unsupported storage ACL entry")
		}
		var mask uint64
		if api.getMask(entry, &mask) != 0 {
			return errors.New("cannot read storage ACL permissions")
		}
		if mask == 0 || !private && mask & ^uint64(readOnly) == 0 {
			continue
		}
		qualifier := api.getQualifier(entry)
		if qualifier == nil {
			return errors.New("cannot read storage ACL identity")
		}
		identity := *(*[16]byte)(qualifier)
		api.free(qualifier)
		var user, root [16]byte
		if api.uidToUUID(uint32(os.Geteuid()), &user) != 0 || api.uidToUUID(0, &root) != 0 {
			return errors.New("cannot verify storage ACL identity")
		}
		if identity != user && identity != root {
			return errors.New("storage ACL grants access to another user")
		}
		// Inherit-only grants are checked too: mkdir/files inherit them, and
		// a private snapshot root must not make its future files public.
	}
	return nil
}
