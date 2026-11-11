package configweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPeettyRejectsCoordinatedCDNTampering(t *testing.T) {
	for _, failure := range []string{"modified executable", "invented tag", "publisher unavailable"} {
		t.Run(failure, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.yaml")
			cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
			u := newPeettyUpdates(config)
			u.client = cdn.client(t)
			if err := u.check(context.Background()); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(u.path)
			cdn.versions = []string{"0.3.2"}
			cdn.publisher = map[string]map[string][]byte{
				"v0.3.1": peettyTestFiles("red", false), "v0.3.2": peettyTestFiles("blue", true),
			}
			cdn.files["v0.3.2"] = peettyTestFiles("blue", true)
			switch failure {
			case "modified executable":
				// Even a matching CDN-controlled manifest cannot authorize these bytes.
				cdn.files["v0.3.2"]["cdn/peetty.global.js"] = bytes.ReplaceAll(cdn.files["v0.3.2"]["cdn/peetty.global.js"], []byte("blue"), []byte("evil"))
			case "invented tag":
				delete(cdn.publisher, "v0.3.2")
			case "publisher unavailable":
				cdn.fail = "api.github.com"
			}
			if err := u.check(context.Background()); err == nil {
				t.Fatal("CDN independently authorized an executable")
			}
			if u.snapshot().Version != "v0.3.1" {
				t.Fatal("unverified executable became active")
			}
			after, _ := os.ReadFile(u.path)
			if !bytes.Equal(original, after) {
				t.Fatal("unverified update replaced the trusted offline cache")
			}
		})
	}
}

func TestPeettySourceResolvesAnnotatedTagsAndPinsDeliveryToCommit(t *testing.T) {
	cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, annotated: true, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
	u := newPeettyUpdates(filepath.Join(t.TempDir(), "config.yaml"))
	u.client = cdn.client(t)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if u.snapshot().Commit != peettyTestCommit("v0.3.1") {
		t.Fatal("wrong publisher commit")
	}
	for _, source := range cdn.requests {
		if strings.HasPrefix(source, peettyCDNBase) && !strings.HasPrefix(source, peettyCDNBase+u.snapshot().Commit+"/") {
			t.Fatalf("delivery was not pinned to the source commit: %s", source)
		}
		if strings.Contains(source, "/flat") {
			t.Fatal("CDN manifest was used as source authority")
		}
	}
	cdn.versions, cdn.tagLoop = []string{"0.3.2"}, true
	cdn.files["v0.3.2"] = peettyTestFiles("blue", true)
	if err := u.check(context.Background()); err == nil {
		t.Fatal("unbounded annotated tag chain accepted")
	}
}

func TestPeettyRejectsLegacySelfConsistentCDNCache(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	cdn := &peettyCDNFixture{versions: []string{"0.3.1"}, files: map[string]map[string][]byte{"v0.3.1": peettyTestFiles("red", false)}}
	u := newPeettyUpdates(config)
	u.client = cdn.client(t)
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(u.path)
	var legacy peettyRelease
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Format, legacy.Commit = 0, ""
	legacy.Files["cdn/peetty.global.js"] = append(legacy.Files["cdn/peetty.global.js"], []byte(";malicious();")...)
	digest := sha256.Sum256(legacy.Files["cdn/peetty.global.js"])
	legacy.Hashes["cdn/peetty.global.js"] = base64.StdEncoding.EncodeToString(digest[:])
	data, _ = json.Marshal(legacy)
	if err := os.WriteFile(u.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if newPeettyUpdates(config).snapshot() != nil {
		t.Fatal("a legacy CDN-supplied digest authenticated cached code")
	}
}
