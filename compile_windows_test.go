//go:build windows

package tailsplit

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestWindowsTUNUsesUserspaceTCP(t *testing.T) {
	for _, captureTraffic := range []bool{false, true} {
		t.Run(fmt.Sprint(captureTraffic), func(t *testing.T) {
			config, err := loadTestConfig(t, fmt.Sprintf("dns:\n  direct: 1.1.1.1\ntun:\n  enabled: %t\n", captureTraffic))
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			var native map[string]any
			if err := json.Unmarshal(compiled.JSON, &native); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, value := range native["inbounds"].([]any) {
				inbound := value.(map[string]any)
				if inbound["type"] == "tun" {
					found = true
					if inbound["stack"] != "gvisor" {
						t.Fatal("Windows TUN must handle both TCP and UDP in userspace")
					}
					if !captureTraffic && inbound["dns_mode"] != "disabled" {
						t.Fatal("proxy-only mode must preserve DNS-only routing")
					}
				}
			}
			if !found {
				t.Fatal("Windows TUN missing")
			}
		})
	}
}
