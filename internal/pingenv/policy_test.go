package pingenv

import (
	"reflect"
	"testing"
)

func TestPingEnvironmentRemovesImplicitLoginSettings(t *testing.T) {
	env := []string{"PATH=/bin", "TS_CONTROL_URL=https://control.example", "TS_AUTHKEY=fixture", "ts_auth_key=fixture", "TS_CLIENT_SECRET=fixture", "TS_CLIENT_ID=fixture", "TS_ID_TOKEN=fixture", "TS_AUDIENCE=fixture", "TSNET_FORCE_LOGIN=1", "TS_AUTHKEY_SUFFIX=keep", "TS_AUTH_KEY="}
	if !HasAuthentication(env) {
		t.Fatal("implicit login settings were not detected")
	}
	want := []string{"PATH=/bin", "TS_CONTROL_URL=https://control.example", "TS_AUTHKEY_SUFFIX=keep", "TS_AUTH_KEY="}
	clean := WithoutAuthentication(env)
	if !reflect.DeepEqual(clean, want) || HasAuthentication(clean) {
		t.Fatalf("unsafe or incomplete ping environment: %v", clean)
	}
	if len(env) != 11 {
		t.Fatal("the original process environment was modified")
	}
}
