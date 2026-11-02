//go:build darwin && cgo

package tailsplit

/*
#cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation
#include <SystemConfiguration/SystemConfiguration.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

static SCDynamicStoreRef tailsplit_install_dns(const char *address, int port,
                                             int pid, int *errorCode) {
    SCDynamicStoreRef store = SCDynamicStoreCreate(NULL, CFSTR("TailSplit DNS"),
                                                 NULL, NULL);
    if (!store) {
        *errorCode = SCError();
        return NULL;
    }
    CFStringRef server = CFStringCreateWithCString(NULL, address,
                                                  kCFStringEncodingUTF8);
    CFArrayRef servers = CFArrayCreate(NULL, (const void **)&server, 1,
                                       &kCFTypeArrayCallBacks);
    CFStringRef match = CFSTR("");
    CFArrayRef domains = CFArrayCreate(NULL, (const void **)&match, 1,
                                       &kCFTypeArrayCallBacks);
    int priority = 1;
    CFNumberRef order = CFNumberCreate(NULL, kCFNumberIntType, &priority);
    CFNumberRef numberPort = CFNumberCreate(NULL, kCFNumberIntType, &port);
    CFArrayRef orders = CFArrayCreate(NULL, (const void **)&order, 1,
                                      &kCFTypeArrayCallBacks);
    const void *keys[] = {
        kSCPropNetDNSServerAddresses, kSCPropNetDNSServerPort,
        kSCPropNetDNSSupplementalMatchDomains,
        kSCPropNetDNSSupplementalMatchOrders,
        kSCPropNetDNSSearchOrder
    };
    const void *values[] = { servers, numberPort, domains, orders, order };
    CFDictionaryRef dns = CFDictionaryCreate(NULL, keys, values, 5,
                                             &kCFTypeDictionaryKeyCallBacks,
                                             &kCFTypeDictionaryValueCallBacks);
    CFStringRef key = CFStringCreateWithFormat(
        NULL, NULL, CFSTR("State:/Network/Service/im.lbv.tailsplit.%d/DNS"), pid);
    // This creates our own ephemeral key. It never overwrites a Wi-Fi, VPN,
    // Tailscale, or persistent Setup:/Network preference.
    Boolean added = SCDynamicStoreAddTemporaryValue(store, key, dns);
    *errorCode = added ? 0 : SCError();
    CFRelease(key);
    CFRelease(dns);
    CFRelease(orders);
    CFRelease(numberPort);
    CFRelease(order);
    CFRelease(domains);
    CFRelease(servers);
    CFRelease(server);
    if (!added) {
        CFRelease(store);
        return NULL;
    }
    return store;
}

static void tailsplit_remove_dns(SCDynamicStoreRef store) {
    // configd removes temporary keys when this session closes, including
    // when the process is killed before Go cleanup can run.
    CFRelease(store);
}
*/
import "C"

import (
	"fmt"
	"net/netip"
	"os"
	"sync"
	"unsafe"
)

func installSystemDNS(listen string) (func() error, error) {
	address, err := netip.ParseAddrPort(listen)
	if err != nil || !validSystemDNSListener(address) {
		return nil, fmt.Errorf("system DNS requires TailSplit's TUN DNS peer on port 53")
	}
	server := C.CString(address.Addr().String())
	defer C.free(unsafe.Pointer(server))
	var code C.int
	store := C.tailsplit_install_dns(server, C.int(address.Port()), C.int(os.Getpid()), &code)
	if store == 0 {
		return nil, fmt.Errorf("install temporary macOS DNS resolver: SystemConfiguration error %d", int(code))
	}
	var once sync.Once
	return func() error {
		once.Do(func() { C.tailsplit_remove_dns(store) })
		return nil
	}, nil
}
