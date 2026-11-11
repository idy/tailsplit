package configweb

import (
	"bytes"
	"strconv"
)

// Give each createPeetty call its own document facade. Reporting hosts at
// creation preserves their owner across arbitrary asynchronous mount work,
// including hosts created after cancellation. Native methods keep the real
// document as receiver, and page geometry remains available to the SDK.
func peettyBrowserBundle(bundle []byte, global string) []byte {
	var script bytes.Buffer
	script.WriteString(`(() => {
  'use strict';
  const root = document;
  const entry = root.currentScript;
  function build(onHost) {
    const scoped = new Proxy(root, {
      get(target, key) {
        if (key === 'currentScript') return entry;
        const value = Reflect.get(target, key, target);
        if (key === 'createElement' || key === 'createElementNS') {
          return (...args) => {
            const element = Reflect.apply(value, target, args);
            if (element.localName === 'peetty-companion') onHost(element);
            return element;
          };
        }
        return typeof value === 'function' ? value.bind(target) : value;
      },
    });
    return ((document) => {
`)
	script.Write(bundle)
	script.WriteString(`
      return Peetty;
    })(scoped);
  }
  const base = build(() => {});
  window[`)
	script.WriteString(strconv.Quote(global))
	script.WriteString(`] = {
    ...base,
    createPeetty(options, onHost = () => {}) {
      return build(onHost).createPeetty(options);
    },
  };
})();
`)
	return script.Bytes()
}

func (release *peettyRelease) browserScript() []byte {
	return peettyBrowserBundle(release.Files["cdn/peetty.global.js"], "Peetty")
}
