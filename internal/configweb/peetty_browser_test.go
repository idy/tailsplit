package configweb

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestPeettyBrowserFacadeKeepsCreationCallbacksWithEachInstance(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	bundle := []byte(`var Peetty = { marker: 42, createPeetty() { return {
      create() { return document.createElement("peetty-companion"); },
      createNS() { return document.createElementNS("http://www.w3.org/1999/xhtml", "peetty-companion"); },
      entry() { return document.currentScript; },
      listen() { document.addEventListener("probe", () => {}); },
    }; } };`)
	runner := `const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const entry={src:'/sdk.js'},doc={currentScript:entry,
createElement(name){assert.equal(this,doc);return {localName:name};},
createElementNS(_,name){assert.equal(this,doc);return {localName:name};},
addEventListener(){assert.equal(this,doc);}};
const window={};vm.runInNewContext(fs.readFileSync(0,'utf8'),{window,document:doc});
doc.currentScript=null;const first=[],second=[];
const a=window.Peetty.createPeetty({},host=>first.push(host));
const b=window.Peetty.createPeetty({},host=>second.push(host));
Promise.resolve().then(()=>{const bh=b.create(),ah=a.create(),ns=a.createNS();
assert.deepEqual(first,[ah,ns]);assert.deepEqual(second,[bh]);
assert.equal(a.entry(),entry);a.listen();assert.equal(window.Peetty.marker,42);
assert.notEqual(a,b);});`
	command := exec.Command(node, "-e", runner)
	command.Stdin = bytes.NewReader(peettyBrowserBundle(bundle, "Peetty"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("SDK facade: %v\n%s", err, output)
	}
}
