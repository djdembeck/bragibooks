// Shared mock classes for DOM testing

class MockClassList {
  constructor(parent) {
    this.parent = parent;
    this.classes = new Set();
  }
  contains(className) {
    return this.classes.has(className);
  }
  add(className) {
    this.classes.add(className);
    if (this.parent)
      this.parent._className = Array.from(this.classes).join(" ");
  }
  remove(className) {
    this.classes.delete(className);
    if (this.parent)
      this.parent._className = Array.from(this.classes).join(" ");
  }
  toggle(className) {
    if (this.classes.has(className)) {
      this.classes.delete(className);
    } else {
      this.classes.add(className);
    }
    if (this.parent)
      this.parent._className = Array.from(this.classes).join(" ");
    return this.classes.has(className);
  }
}

function buildQuerySelectorAllResults(selector, rootChildren) {
  const results = [];
  if (selector === ".tab") {
    for (const child of rootChildren) {
      if (child.classList && child.classList.contains("tab"))
        results.push(child);
    }
    return results;
  }
  if (selector === ".tab-pane") {
    for (const child of rootChildren) {
      if (child.classList && child.classList.contains("tab-pane"))
        results.push(child);
    }
    return results;
  }
  if (selector === '.tab a[role="tab"]') {
    for (const tab of rootChildren) {
      if (tab.classList && tab.classList.contains("tab")) {
        for (const child of tab.children) {
          if (child.getAttribute("role") === "tab") results.push(child);
        }
      }
    }
    return results;
  }
  if (selector === ".tabs") {
    for (const child of rootChildren) {
      if (child.classList && child.classList.contains("tabs"))
        results.push(child);
    }
    return results;
  }
  // Handle .arrow selector (importer.test.js)
  if (selector === ".arrow") {
    for (const child of rootChildren) {
      if (child.classList && child.classList.contains("arrow"))
        results.push(child);
    }
    return results;
  }
  // Handle .arrow i selector (importer.test.js)
  if (selector === ".arrow i") {
    for (const child of rootChildren) {
      if (child.classList && child.classList.contains("arrow")) {
        for (const subChild of child.children) {
          if (subChild.tagName === "i") results.push(subChild);
        }
      }
    }
    return results;
  }
  // Handle label selector (importer.test.js)
  if (selector === "label") {
    for (const child of rootChildren) {
      if (child.tagName === "label") results.push(child);
    }
    return results;
  }
  // Handle label.panel-block[folder-id=""] selector (importer.test.js)
  if (selector === 'label.panel-block[folder-id=""]') {
    for (const child of rootChildren) {
      if (
        child.tagName === "label" &&
        child.classList &&
        child.classList.contains("panel-block")
      ) {
        const attr = child.getAttribute("folder-id");
        if (attr === "") results.push(child);
      }
    }
    return results;
  }
  // Handle label.panel-block:not([folder-id=""]) selector (importer.test.js)
  if (selector === 'label.panel-block:not([folder-id=""])') {
    for (const child of rootChildren) {
      if (
        child.tagName === "label" &&
        child.classList &&
        child.classList.contains("panel-block")
      ) {
        const attr = child.getAttribute("folder-id");
        if (attr && attr !== "") results.push(child);
      }
    }
    return results;
  }
  // Handle folder-id^= prefix selector (importer.test.js)
  if (selector.includes("[folder-id^=")) {
    const match = selector.match(/folder-id\^='([^']+)'/);
    if (match) {
      const folderId = match[1];
      for (const child of rootChildren) {
        if (child.classList && child.classList.contains("panel-block")) {
          const attr = child.getAttribute("folder-id");
          if (attr && attr.startsWith(folderId)) results.push(child);
        }
      }
    }
    return results;
  }
  // Handle .panel-block selector (importer.test.js)
  if (selector === ".panel-block") {
    for (const child of rootChildren) {
      if (child.classList && child.classList.contains("panel-block"))
        results.push(child);
    }
    return results;
  }
  return results;
}

class MockElement {
  constructor(tagName = "div") {
    this.tagName = tagName;
    this.style = {};
    this.children = [];
    this.classList = new MockClassList(this);
    this.eventListeners = new Map();
    this.textContent = "";
    this.attributes = new Map();
    this.parentElement = null;
    this.id = "";
    this._className = "";
    this.dataset = {};
  }

  set className(value) {
    this._className = value;
    this.classList.classes.clear();
    value.split(/\s+/).forEach((cls) => {
      if (cls) this.classList.classes.add(cls);
    });
  }

  get className() {
    return this._className;
  }

  matches(selector) {
    if (selector.startsWith(".")) {
      const parts = selector.slice(1).split(/[\s\[]/);
      return this.classList.contains(parts[0]);
    }
    if (selector === this.tagName) return true;
    if (selector.startsWith("#")) {
      return this.attributes.get("id") === selector.slice(1);
    }
    console.warn("Unsupported selector pattern:", selector);
    return false;
  }

  querySelector(selector) {
    // Handle .arrow i selector from importer.test.js
    if (selector === ".arrow i") {
      for (const child of this.children) {
        if (child.classList && child.classList.contains("arrow")) {
          for (const subChild of child.children) {
            if (subChild.tagName === "i") return subChild;
          }
        }
      }
      return null;
    }
    // Handle attribute selectors
    if (selector.startsWith(".")) {
      const className = selector.slice(1);
      for (const child of this.children) {
        if (child.classList && child.classList.contains(className))
          return child;
      }
    }
    if (selector.startsWith("[") && selector.endsWith("]")) {
      const attrName = selector.slice(1, -1);
      for (const child of this.children) {
        if (child.getAttribute(attrName)) return child;
      }
    }
    return null;
  }

  querySelectorAll(selector) {
    return buildQuerySelectorAllResults(selector, this.children);
  }

  appendChild(child) {
    this.children.push(child);
    child.parentElement = this;
    return child;
  }

  remove() {
    if (this.parentElement) {
      const index = this.parentElement.children.indexOf(this);
      if (index > -1) this.parentElement.children.splice(index, 1);
    }
  }

  addEventListener(event, handler) {
    if (!this.eventListeners.has(event)) this.eventListeners.set(event, []);
    this.eventListeners.get(event).push(handler);
  }

  focus() {
    this._focused = true;
  }

  getAttribute(name) {
    return this.attributes.get(name) ?? null;
  }
  setAttribute(name, value) {
    this.attributes.set(name, value);
  }
}

class MockDocument {
  constructor() {
    this.elements = new Map();
    this._mockElements = [];
    this._activeElement = null;
  }

  getElementById(id) {
    return this.elements.get(id) || null;
  }
  setElement(id, element) {
    this.elements.set(id, element);
    element.id = id;
    if (!this._mockElements.includes(element)) {
      this._mockElements.push(element);
    }
  }
  addMockElement(element) {
    this._mockElements.push(element);
  }

  get activeElement() {
    return this._activeElement;
  }
  set activeElement(element) {
    this._activeElement = element;
  }

  querySelector(selector) {
    // Handle .tabs selector from book_tabs.test.js
    if (selector === ".tabs") {
      for (const el of this._mockElements) {
        if (el.classList && el.classList.contains("tabs")) return el;
      }
      return null;
    }
    // Handle .tab selector from book_tabs.test.js
    if (selector === ".tab") {
      for (const el of this._mockElements) {
        if (el.classList && el.classList.contains("tab")) return el;
      }
      return null;
    }
    // Handle folder selector from importer.test.js
    const folderMatch = selector.match(/\.folder\[id\^=['"]([^'"]+)['"]\]/);
    if (folderMatch) {
      const folderId = folderMatch[1];
      for (const el of this._mockElements) {
        if (el.classList.contains("folder")) {
          const id = el.getAttribute("id");
          if (id && id.startsWith(folderId)) return el;
        }
      }
      return null;
    }
    // Handle .panel-block-container selector
    if (selector === ".panel-block-container") {
      for (const el of this._mockElements) {
        if (el.classList.contains("panel-block-container")) return el;
      }
      return null;
    }
    // Handle .arrow i selector
    if (selector === ".arrow i") {
      for (const el of this._mockElements) {
        if (el.classList.contains("arrow")) {
          for (const child of el.children) {
            if (child.tagName === "i") return child;
          }
        }
      }
      return null;
    }
    // Handle #search-input selector
    if (selector === "#search-input") {
      return this.getElementById("search-input");
    }
    // Handle .clear-search selector
    if (selector === ".clear-search") {
      for (const el of this._mockElements) {
        if (el.classList.contains("clear-search")) return el;
      }
      return null;
    }
    return null;
  }

  querySelectorAll(selector) {
    return buildQuerySelectorAllResults(selector, this._mockElements);
  }

  createElement(tagName) {
    return new MockElement(tagName);
  }
}

module.exports = {
  MockElement,
  MockClassList,
  MockDocument,
  buildQuerySelectorAllResults,
};
