package privatefs

import (
	"errors"
	"os"
	"runtime"
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
		{"acl_get_file", &api.getFile},
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
	getFile      func(string, int32) unsafe.Pointer
	errno        func() *int32
	getEntry     func(unsafe.Pointer, int32, *unsafe.Pointer) int32
	getTag       func(unsafe.Pointer, *int32) int32
	getMask      func(unsafe.Pointer, *uint64) int32
	getQualifier func(unsafe.Pointer) unsafe.Pointer
	free         func(unsafe.Pointer) int32
	uidToUUID    func(uint32, *[16]byte) int32
}

func checkDirectoryACL(path string, private bool) error {
	api, err := darwinACL()
	if err != nil {
		return err
	}
	// errno is thread-local; keep these two native calls on the same thread.
	runtime.LockOSThread()
	acl := api.getFile(path, 0x100) // ACL_TYPE_EXTENDED
	var aclErr syscall.Errno
	if acl == nil {
		aclErr = syscall.Errno(*api.errno())
	}
	runtime.UnlockOSThread()
	if acl == nil {
		// Darwin also returns ENOENT for an existing directory with no ACL.
		// Recheck existence/type so a vanished path is never mistaken for that.
		if aclErr == syscall.ENOENT || aclErr == syscall.ENOTSUP {
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("ACL path must remain a directory")
			}
			return nil
		}
		return &os.PathError{Op: "read ACL", Path: path, Err: aclErr}
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
			return errors.New("directory ACL exceeds the supported entry bound")
		}
		var tag int32
		if api.getTag(entry, &tag) != 0 {
			return errors.New("cannot read directory ACL entry")
		}
		if tag == 2 { // ACL_EXTENDED_DENY, including the usual deny-delete ACL.
			continue
		}
		if tag != 1 { // ACL_EXTENDED_ALLOW
			return errors.New("unsupported directory ACL entry")
		}
		var mask uint64
		if api.getMask(entry, &mask) != 0 {
			return errors.New("cannot read directory ACL permissions")
		}
		if mask == 0 || !private && mask & ^uint64(readOnly) == 0 {
			continue
		}
		qualifier := api.getQualifier(entry)
		if qualifier == nil {
			return errors.New("cannot read directory ACL identity")
		}
		identity := *(*[16]byte)(qualifier)
		api.free(qualifier)
		var user, root [16]byte
		if api.uidToUUID(uint32(os.Geteuid()), &user) != 0 || api.uidToUUID(0, &root) != 0 {
			return errors.New("cannot verify directory ACL identity")
		}
		if identity != user && identity != root {
			return errors.New("directory ACL grants access to another user")
		}
		// Inherit-only grants are checked too: mkdir/files inherit them, and
		// a private snapshot root must not make its future files public.
	}
	return nil
}
