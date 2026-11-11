package configweb

import (
	"bytes"
	"strconv"
)

// Each SDK instance renders into a private custom-element tag. DOM cloning,
// importing and serialization preserve that immutable tag, so copied hosts
// retain their instance identity even when they bypass createElement callbacks.
// Native methods keep the real document receiver and page geometry stays intact.
func peettyBrowserBundle(bundle []byte, global string) []byte {
	var script bytes.Buffer
	script.WriteString(`(() => {
  'use strict';
  const root = document;
  const entry = root.currentScript;
  const originalTag = 'peetty-companion';
  function build(onHost, hostTag) {
    const scoped = new Proxy(root, {
      get(target, key) {
        if (key === 'currentScript') return entry;
        const value = Reflect.get(target, key, target);
        if (key === 'createElement' || key === 'createElementNS') {
          return (...args) => {
            const index = key === 'createElement' ? 0 : 1;
            if (typeof args[index] === 'string' && args[index].toLowerCase() === originalTag) args[index] = hostTag;
            const element = Reflect.apply(value, target, args);
            if (element.localName === hostTag) onHost(element);
            return element;
          };
        }
        if (key === 'querySelector' || key === 'querySelectorAll' || key === 'getElementsByTagName') {
          return (...args) => {
            if (typeof args[0] === 'string' && args[0].toLowerCase() === originalTag) args[0] = hostTag;
            return Reflect.apply(value, target, args);
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
  const base = build(() => {}, originalTag);
  window[`)
	script.WriteString(strconv.Quote(global))
	script.WriteString(`] = {
    ...base,
    createPeetty(options, onHost = () => {}, hostTag = originalTag) {
      return build(onHost, hostTag).createPeetty(options);
    },
  };
})();
`)
	return script.Bytes()
}

func (release *peettyRelease) browserScript() []byte {
	return peettyBrowserBundle(release.Files["cdn/peetty.global.js"], "Peetty")
}
