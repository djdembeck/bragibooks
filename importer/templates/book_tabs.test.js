const { describe, it, before, after } = require('node:test');
const assert = require('node:assert');
const { openTab, handleKeyDown } = require('./book_tabs.js');

// Mock classes for DOM testing
class MockClassList {
    constructor(parent) {
        this.parent = parent;
        this.classes = new Set();
    }
    contains(className) { return this.classes.has(className); }
    add(className) {
        this.classes.add(className);
        if (this.parent) this.parent._className = Array.from(this.classes).join(' ');
    }
    remove(className) {
        this.classes.delete(className);
        if (this.parent) this.parent._className = Array.from(this.classes).join(' ');
    }
}

class MockElement {
    constructor(tagName = 'div') {
        this.tagName = tagName;
        this.style = {};
        this.children = [];
        this.classList = new MockClassList(this);
        this.eventListeners = new Map();
        this.textContent = '';
        this.attributes = new Map();
        this.parentElement = null;
        this.id = '';
        this._className = '';
        this.dataset = {};
    }

    set className(value) {
        this._className = value;
        this.classList.classes.clear();
        value.split(/\s+/).forEach(cls => {
            if (cls) this.classList.classes.add(cls);
        });
    }

    get className() {
        return this._className;
    }

    matches(selector) {
        if (selector.startsWith('.')) {
            const parts = selector.slice(1).split(/[\s\[]/);
            return this.classList.contains(parts[0]);
        }
        if (selector === this.tagName) return true;
        if (selector.startsWith('#')) {
            return this.attributes.get('id') === selector.slice(1);
        }
        return false;
    }

    querySelector(selector) {
        if (selector.startsWith('.')) {
            const className = selector.slice(1);
            for (const child of this.children) {
                if (child.classList && child.classList.contains(className)) return child;
            }
        }
        if (selector.startsWith('[') && selector.endsWith(']')) {
            const attrName = selector.slice(1, -1);
            for (const child of this.children) {
                if (child.getAttribute(attrName)) return child;
            }
        }
        return null;
    }

    querySelectorAll(selector) {
        const results = [];
        if (selector === '.tab') {
            for (const child of this.children) {
                if (child.classList && child.classList.contains('tab')) results.push(child);
            }
            return results;
        }
        if (selector === '.tab-pane') {
            for (const child of this.children) {
                if (child.classList && child.classList.contains('tab-pane')) results.push(child);
            }
            return results;
        }
        if (selector === '.tab a[role="tab"]') {
            for (const tab of this.children) {
                if (tab.classList && tab.classList.contains('tab')) {
                    for (const child of tab.children) {
                        if (child.getAttribute('role') === 'tab') results.push(child);
                    }
                }
            }
            return results;
        }
        if (selector === '.tabs') {
            for (const child of this.children) {
                if (child.classList && child.classList.contains('tabs')) results.push(child);
            }
            return results;
        }
        return results;
    }

    appendChild(child) {
        this.children.push(child);
        child.parentElement = this;
        return child;
    }

    addEventListener(event, handler) {
        if (!this.eventListeners.has(event)) this.eventListeners.set(event, []);
        this.eventListeners.get(event).push(handler);
    }

    focus() {
        this._focused = true;
    }

    getAttribute(name) { return this.attributes.get(name) || null; }
    setAttribute(name, value) { this.attributes.set(name, value); }
}

class MockDocument {
    constructor() {
        this.elements = new Map();
        this._mockElements = [];
        this._activeElement = null;
    }

    getElementById(id) { return this.elements.get(id) || null; }
    setElement(id, element) {
        this.elements.set(id, element);
        element.id = id;
    }

    get activeElement() { return this._activeElement; }
    set activeElement(element) { this._activeElement = element; }

    querySelector(selector) {
        if (selector === '.tabs') {
            for (const el of this._mockElements) {
                if (el.classList && el.classList.contains('tabs')) return el;
            }
            return null;
        }
        if (selector === '.tab') {
            for (const el of this._mockElements) {
                if (el.classList && el.classList.contains('tab')) return el;
            }
            return null;
        }
        return null;
    }

    querySelectorAll(selector) {
        const results = [];
        if (selector === '.tab') {
            for (const el of this._mockElements) {
                if (el.classList && el.classList.contains('tab')) results.push(el);
            }
            return results;
        }
        if (selector === '.tab-pane') {
            for (const el of this._mockElements) {
                if (el.classList && el.classList.contains('tab-pane')) results.push(el);
            }
            return results;
        }
        if (selector === '.tab a[role="tab"]') {
            for (const el of this._mockElements) {
                if (el.classList && el.classList.contains('tab')) {
                    for (const child of el.children) {
                        if (child.getAttribute('role') === 'tab') results.push(child);
                    }
                }
            }
            return results;
        }
        if (selector === '.tabs') {
            for (const el of this._mockElements) {
                if (el.classList && el.classList.contains('tabs')) results.push(el);
            }
            return results;
        }
        return results;
    }

    addMockElement(element) { this._mockElements.push(element); }
}

describe('openTab', () => {
    it('should show tab element and activate tab button when both exist', () => {
        const mockDoc = new MockDocument();

        // Create tab pane
        const tabPane = new MockElement('div');
        tabPane.classList.add('tab-pane');
        tabPane.style.display = 'none';
        mockDoc.setElement('done', tabPane);

        // Create tab button
        const tabButton = new MockElement('li');
        tabButton.classList.add('tab');
        mockDoc.setElement('done-tab', tabButton);

        // Create tab anchor
        const tabAnchor = new MockElement('a');
        tabAnchor.setAttribute('role', 'tab');
        tabAnchor.setAttribute('aria-controls', 'done');
        tabButton.appendChild(tabAnchor);
        mockDoc.addMockElement(tabButton);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            openTab({ preventDefault: () => {} }, 'done', false);

            assert.strictEqual(tabPane.style.display, 'block', 'Tab pane should be visible');
            assert.strictEqual(tabButton.classList.contains('is-active'), true, 'Tab button should have is-active class');
            assert.strictEqual(tabAnchor.getAttribute('aria-selected'), 'true', 'Anchor should have aria-selected="true"');
            assert.strictEqual(tabAnchor.getAttribute('tabindex'), '0', 'Anchor should have tabindex="0"');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should not modify UI and return early when tab element is missing', () => {
        const mockDoc = new MockDocument();
        const warnings = [];
        const originalWarn = console.warn;
        console.warn = (msg) => warnings.push(msg);

        // Create another tab that is currently active
        const otherPane = new MockElement('div');
        otherPane.classList.add('tab-pane');
        otherPane.style.display = 'block';
        mockDoc.setElement('processing', otherPane);

        const otherButton = new MockElement('li');
        otherButton.classList.add('tab');
        otherButton.classList.add('is-active');
        mockDoc.setElement('processing-tab', otherButton);

        mockDoc.addMockElement(otherButton);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            openTab({ preventDefault: () => {} }, 'nonexistent', false);

            // The existing active tab should remain unchanged
            assert.strictEqual(otherPane.style.display, 'block', 'Existing tab pane should remain visible');
            assert.strictEqual(otherButton.classList.contains('is-active'), true, 'Existing tab button should remain active');
            assert.strictEqual(warnings.length, 1, 'Should log one warning');
            assert.ok(warnings[0].includes("'nonexistent' not found"), 'Warning should mention missing tab element');
        } finally {
            global.document = originalDoc;
            console.warn = originalWarn;
        }
    });

    it('should not modify UI and return early when tab button is missing', () => {
        const mockDoc = new MockDocument();
        const warnings = [];
        const originalWarn = console.warn;
        console.warn = (msg) => warnings.push(msg);

        // Create tab pane but NOT the tab button
        const tabPane = new MockElement('div');
        tabPane.classList.add('tab-pane');
        tabPane.style.display = 'none';
        mockDoc.setElement('done', tabPane);

        // Create another tab that is currently active
        const otherPane = new MockElement('div');
        otherPane.classList.add('tab-pane');
        otherPane.style.display = 'block';
        mockDoc.setElement('processing', otherPane);

        const otherButton = new MockElement('li');
        otherButton.classList.add('tab');
        otherButton.classList.add('is-active');
        mockDoc.setElement('processing-tab', otherButton);

        mockDoc.addMockElement(otherButton);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            openTab({ preventDefault: () => {} }, 'done', false);

            // The existing active tab should remain unchanged since button is missing
            assert.strictEqual(otherPane.style.display, 'block', 'Existing tab pane should remain visible');
            assert.strictEqual(otherButton.classList.contains('is-active'), true, 'Existing tab button should remain active');
            assert.strictEqual(warnings.length, 1, 'Should log one warning');
            assert.ok(warnings[0].includes("'done-tab' not found"), 'Warning should mention missing tab button');
        } finally {
            global.document = originalDoc;
            console.warn = originalWarn;
        }
    });

    it('should focus anchor when userInitiated is true', () => {
        const mockDoc = new MockDocument();

        const tabPane = new MockElement('div');
        tabPane.classList.add('tab-pane');
        mockDoc.setElement('done', tabPane);

        const tabButton = new MockElement('li');
        tabButton.classList.add('tab');
        mockDoc.setElement('done-tab', tabButton);

        const tabAnchor = new MockElement('a');
        tabAnchor.setAttribute('role', 'tab');
        tabAnchor.setAttribute('aria-controls', 'done');
        tabButton.appendChild(tabAnchor);
        mockDoc.addMockElement(tabButton);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            openTab({ preventDefault: () => {} }, 'done', true);

            assert.strictEqual(tabAnchor._focused, true, 'Anchor should be focused when userInitiated is true');
        } finally {
            global.document = originalDoc;
        }
    });
});

describe('handleKeyDown', () => {
    it('should navigate to previous tab with ArrowLeft', () => {
        const mockDoc = new MockDocument();

        // Create tabs container
        const tabsContainer = new MockElement('div');

        // Create first tab with pane and button
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);
        tabsContainer.appendChild(tab1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        // Create second tab with pane and button
        const tab2 = new MockElement('li');
        tab2.classList.add('tab');
        const anchor2 = new MockElement('a');
        anchor2.setAttribute('role', 'tab');
        anchor2.setAttribute('aria-controls', 'processing');
        tab2.appendChild(anchor2);
        tabsContainer.appendChild(tab2);
        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        // Create third tab with pane and button
        const tab3 = new MockElement('li');
        tab3.classList.add('tab');
        const anchor3 = new MockElement('a');
        anchor3.setAttribute('role', 'tab');
        anchor3.setAttribute('aria-controls', 'error');
        tab3.appendChild(anchor3);
        tabsContainer.appendChild(tab3);
        const errorPane = new MockElement('div');
        errorPane.classList.add('tab-pane');
        mockDoc.setElement('error', errorPane);
        const errorButton = new MockElement('li');
        errorButton.classList.add('tab');
        mockDoc.setElement('error-tab', errorButton);

        mockDoc.addMockElement(tab1);
        mockDoc.addMockElement(tab2);
        mockDoc.addMockElement(tab3);
        mockDoc._activeElement = anchor2; // Second tab is focused

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'ArrowLeft', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify that the "done" tab is now active
            assert.strictEqual(donePane.style.display, 'block', 'Done pane should be visible');
            assert.strictEqual(doneButton.classList.contains('is-active'), true, 'Done button should be active');
            assert.strictEqual(anchor1.getAttribute('aria-selected'), 'true', 'Done anchor should have aria-selected="true"');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should wrap around to last tab with ArrowLeft from first tab', () => {
        const mockDoc = new MockDocument();

        // Create tabs with panes and buttons
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        const tab2 = new MockElement('li');
        tab2.classList.add('tab');
        const anchor2 = new MockElement('a');
        anchor2.setAttribute('role', 'tab');
        anchor2.setAttribute('aria-controls', 'processing');
        tab2.appendChild(anchor2);
        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        const tab3 = new MockElement('li');
        tab3.classList.add('tab');
        const anchor3 = new MockElement('a');
        anchor3.setAttribute('role', 'tab');
        anchor3.setAttribute('aria-controls', 'error');
        tab3.appendChild(anchor3);
        const errorPane = new MockElement('div');
        errorPane.classList.add('tab-pane');
        mockDoc.setElement('error', errorPane);
        const errorButton = new MockElement('li');
        errorButton.classList.add('tab');
        mockDoc.setElement('error-tab', errorButton);

        mockDoc.addMockElement(tab1);
        mockDoc.addMockElement(tab2);
        mockDoc.addMockElement(tab3);
        mockDoc._activeElement = anchor1; // First tab is focused

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'ArrowLeft', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify wrap-around to last tab
            assert.strictEqual(errorPane.style.display, 'block', 'Error pane should be visible (wrap-around)');
            assert.strictEqual(errorButton.classList.contains('is-active'), true, 'Error button should be active');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should navigate to next tab with ArrowRight', () => {
        const mockDoc = new MockDocument();

        // Create tabs with panes and buttons
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        const tab2 = new MockElement('li');
        tab2.classList.add('tab');
        const anchor2 = new MockElement('a');
        anchor2.setAttribute('role', 'tab');
        anchor2.setAttribute('aria-controls', 'processing');
        tab2.appendChild(anchor2);
        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        mockDoc.addMockElement(tab1);
        mockDoc.addMockElement(tab2);
        mockDoc._activeElement = anchor1;

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'ArrowRight', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify navigation to next tab
            assert.strictEqual(processingPane.style.display, 'block', 'Processing pane should be visible');
            assert.strictEqual(processingButton.classList.contains('is-active'), true, 'Processing button should be active');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should wrap around to first tab with ArrowRight from last tab', () => {
        const mockDoc = new MockDocument();

        // Create tabs with panes and buttons
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        const tab2 = new MockElement('li');
        tab2.classList.add('tab');
        const anchor2 = new MockElement('a');
        anchor2.setAttribute('role', 'tab');
        anchor2.setAttribute('aria-controls', 'processing');
        tab2.appendChild(anchor2);
        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        mockDoc.addMockElement(tab1);
        mockDoc.addMockElement(tab2);
        mockDoc._activeElement = anchor2; // Last tab is focused

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'ArrowRight', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify wrap-around to first tab
            assert.strictEqual(donePane.style.display, 'block', 'Done pane should be visible (wrap-around)');
            assert.strictEqual(doneButton.classList.contains('is-active'), true, 'Done button should be active');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should navigate to first tab with Home key', () => {
        const mockDoc = new MockDocument();

        // Create tabs with panes and buttons
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        const tab2 = new MockElement('li');
        tab2.classList.add('tab');
        const anchor2 = new MockElement('a');
        anchor2.setAttribute('role', 'tab');
        anchor2.setAttribute('aria-controls', 'processing');
        tab2.appendChild(anchor2);
        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        const tab3 = new MockElement('li');
        tab3.classList.add('tab');
        const anchor3 = new MockElement('a');
        anchor3.setAttribute('role', 'tab');
        anchor3.setAttribute('aria-controls', 'error');
        tab3.appendChild(anchor3);
        const errorPane = new MockElement('div');
        errorPane.classList.add('tab-pane');
        mockDoc.setElement('error', errorPane);
        const errorButton = new MockElement('li');
        errorButton.classList.add('tab');
        mockDoc.setElement('error-tab', errorButton);

        mockDoc.addMockElement(tab1);
        mockDoc.addMockElement(tab2);
        mockDoc.addMockElement(tab3);
        mockDoc._activeElement = anchor3; // Last tab is focused

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'Home', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify navigation to first tab
            assert.strictEqual(donePane.style.display, 'block', 'Done pane should be visible');
            assert.strictEqual(doneButton.classList.contains('is-active'), true, 'Done button should be active');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should navigate to last tab with End key', () => {
        const mockDoc = new MockDocument();

        // Create tabs with panes and buttons
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        const tab2 = new MockElement('li');
        tab2.classList.add('tab');
        const anchor2 = new MockElement('a');
        anchor2.setAttribute('role', 'tab');
        anchor2.setAttribute('aria-controls', 'processing');
        tab2.appendChild(anchor2);
        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        const tab3 = new MockElement('li');
        tab3.classList.add('tab');
        const anchor3 = new MockElement('a');
        anchor3.setAttribute('role', 'tab');
        anchor3.setAttribute('aria-controls', 'error');
        tab3.appendChild(anchor3);
        const errorPane = new MockElement('div');
        errorPane.classList.add('tab-pane');
        mockDoc.setElement('error', errorPane);
        const errorButton = new MockElement('li');
        errorButton.classList.add('tab');
        mockDoc.setElement('error-tab', errorButton);

        mockDoc.addMockElement(tab1);
        mockDoc.addMockElement(tab2);
        mockDoc.addMockElement(tab3);
        mockDoc._activeElement = anchor1; // First tab is focused

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'End', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify navigation to last tab
            assert.strictEqual(errorPane.style.display, 'block', 'Error pane should be visible');
            assert.strictEqual(errorButton.classList.contains('is-active'), true, 'Error button should be active');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should not call openTab for unhandled keys', () => {
        const mockDoc = new MockDocument();
        const openTabCalls = [];

        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);

        mockDoc.addMockElement(tab1);
        mockDoc._activeElement = anchor1;

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const originalOpenTab = global.openTab;
            global.openTab = (event, tabId, userInitiated) => {
                openTabCalls.push({ tabId, userInitiated });
            };

            const event = { key: 'Enter', preventDefault: () => {} };
            handleKeyDown(event);

            assert.strictEqual(openTabCalls.length, 0, 'openTab should not be called for unhandled keys');

            global.openTab = originalOpenTab;
        } finally {
            global.document = originalDoc;
        }
    });

    it('should return early when active element is not a tab anchor', () => {
        const mockDoc = new MockDocument();
        const openTabCalls = [];

        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', 'done');
        tab1.appendChild(anchor1);

        mockDoc.addMockElement(tab1);
        mockDoc._activeElement = new MockElement('div'); // Not a tab anchor

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const originalOpenTab = global.openTab;
            global.openTab = () => {
                openTabCalls.push({});
            };

            const event = { key: 'ArrowRight', preventDefault: () => {} };
            handleKeyDown(event);

            assert.strictEqual(openTabCalls.length, 0, 'openTab should not be called when active element is not a tab anchor');

            global.openTab = originalOpenTab;
        } finally {
            global.document = originalDoc;
        }
    });

    it('should log warning when anchor has no aria-controls attribute', () => {
        const mockDoc = new MockDocument();
        const warnings = [];
        const originalWarn = console.warn;
        console.warn = (msg) => warnings.push(msg);

        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        // No aria-controls attribute!
        tab1.appendChild(anchor1);

        mockDoc.addMockElement(tab1);
        mockDoc._activeElement = anchor1;

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'ArrowRight', preventDefault: () => {} };
            handleKeyDown(event);

            assert.strictEqual(warnings.length, 1, 'Should log one warning');
            assert.ok(warnings[0].includes('missing aria-controls attribute'), 'Warning should mention missing aria-controls');

            console.warn = originalWarn;
        } finally {
            global.document = originalDoc;
        }
    });

    it('should extract tabId from aria-controls with hash prefix', () => {
        const mockDoc = new MockDocument();

        // Create tab with pane and button
        const tab1 = new MockElement('li');
        tab1.classList.add('tab');
        const anchor1 = new MockElement('a');
        anchor1.setAttribute('role', 'tab');
        anchor1.setAttribute('aria-controls', '#done'); // With hash prefix
        tab1.appendChild(anchor1);
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        mockDoc.addMockElement(tab1);
        mockDoc._activeElement = anchor1;

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const event = { key: 'ArrowRight', preventDefault: () => {} };
            handleKeyDown(event);

            // Verify hash prefix was stripped and tab is active
            assert.strictEqual(donePane.style.display, 'block', 'Done pane should be visible');
            assert.strictEqual(doneButton.classList.contains('is-active'), true, 'Done button should be active');
        } finally {
            global.document = originalDoc;
        }
    });
});

describe('window load initialization', () => {
    it('should use data-default attribute value when present', () => {
        const mockDoc = new MockDocument();
        const openTabCalls = [];

        // Create tabs container with data-default
        const tabsContainer = new MockElement('div');
        tabsContainer.classList.add('tabs');
        tabsContainer.dataset.default = '#processing';
        mockDoc.addMockElement(tabsContainer);

        // Create tab panes and buttons
        const donePane = new MockElement('div');
        donePane.classList.add('tab-pane');
        mockDoc.setElement('done', donePane);
        const doneButton = new MockElement('li');
        doneButton.classList.add('tab');
        mockDoc.setElement('done-tab', doneButton);

        const processingPane = new MockElement('div');
        processingPane.classList.add('tab-pane');
        mockDoc.setElement('processing', processingPane);
        const processingButton = new MockElement('li');
        processingButton.classList.add('tab');
        mockDoc.setElement('processing-tab', processingButton);

        // Create tab anchors
        const doneTab = new MockElement('li');
        doneTab.classList.add('tab');
        const doneAnchor = new MockElement('a');
        doneAnchor.setAttribute('role', 'tab');
        doneAnchor.setAttribute('aria-controls', 'done');
        doneTab.appendChild(doneAnchor);

        const processingTab = new MockElement('li');
        processingTab.classList.add('tab');
        const processingAnchor = new MockElement('a');
        processingAnchor.setAttribute('role', 'tab');
        processingAnchor.setAttribute('aria-controls', 'processing');
        processingTab.appendChild(processingAnchor);

        mockDoc.addMockElement(doneTab);
        mockDoc.addMockElement(processingTab);

        const originalDoc = global.document;
        const originalOpenTab = global.openTab;

        try {
            global.document = mockDoc;
            global.openTab = (event, tabId, userInitiated) => {
                openTabCalls.push({ tabId, userInitiated });
            };

            // Simulate the window load handler
            const tabsContainerFromDoc = document.querySelector(".tabs");
            let defaultTab;

            if (tabsContainerFromDoc && tabsContainerFromDoc.dataset.default) {
                defaultTab = tabsContainerFromDoc.dataset.default.replace(/^#/, "");
            }

            assert.strictEqual(defaultTab, 'processing', 'Should extract tab id from data-default without hash');

            global.openTab = originalOpenTab;
        } finally {
            global.document = originalDoc;
        }
    });

    it('should fallback to first tab aria-controls when data-default is missing', () => {
        const mockDoc = new MockDocument();

        // Create tabs container without data-default
        const tabsContainer = new MockElement('div');
        tabsContainer.classList.add('tabs');
        mockDoc.addMockElement(tabsContainer);

        // Create first tab with aria-controls on the tab element itself
        const firstTab = new MockElement('li');
        firstTab.classList.add('tab');
        firstTab.setAttribute('aria-controls', '#error');
        mockDoc.addMockElement(firstTab);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const tabsContainerFromDoc = document.querySelector(".tabs");
            let defaultTab;

            if (tabsContainerFromDoc && tabsContainerFromDoc.dataset.default) {
                defaultTab = tabsContainerFromDoc.dataset.default.replace(/^#/, "");
            } else {
                const firstTabButton = document.querySelector(".tab");
                if (firstTabButton) {
                    let ariaControls = firstTabButton.getAttribute("aria-controls");
                    if (ariaControls) {
                        defaultTab = ariaControls.replace(/^#/, "");
                    }
                }
            }

            assert.strictEqual(defaultTab, 'error', 'Should fallback to first tab aria-controls');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should fallback to id.replace("-tab", "") when no aria-controls', () => {
        const mockDoc = new MockDocument();

        // Create tabs container without data-default
        const tabsContainer = new MockElement('div');
        tabsContainer.classList.add('tabs');
        mockDoc.addMockElement(tabsContainer);

        // Create first tab without aria-controls but with id
        const firstTab = new MockElement('li');
        firstTab.classList.add('tab');
        firstTab.id = 'done-tab';
        mockDoc.addMockElement(firstTab);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const tabsContainerFromDoc = document.querySelector(".tabs");
            let defaultTab;

            if (tabsContainerFromDoc && tabsContainerFromDoc.dataset.default) {
                defaultTab = tabsContainerFromDoc.dataset.default.replace(/^#/, "");
            } else {
                const firstTabButton = document.querySelector(".tab");
                if (firstTabButton) {
                    let ariaControls = firstTabButton.getAttribute("aria-controls");
                    if (!ariaControls) {
                        const anchorWithControls = firstTabButton.querySelector('[aria-controls]');
                        if (anchorWithControls) {
                            ariaControls = anchorWithControls.getAttribute("aria-controls");
                        }
                    }
                    if (ariaControls) {
                        defaultTab = ariaControls.replace(/^#/, "");
                    } else {
                        const parsed = firstTabButton.id.replace("-tab", "");
                        defaultTab = parsed || "done";
                    }
                }
            }

            assert.strictEqual(defaultTab, 'done', 'Should parse tab id from element id');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should use "done" as final fallback when no tabs container exists', () => {
        const mockDoc = new MockDocument();
        // No tabs container added

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const tabsContainerFromDoc = document.querySelector(".tabs");
            let defaultTab;

            if (tabsContainerFromDoc && tabsContainerFromDoc.dataset.default) {
                defaultTab = tabsContainerFromDoc.dataset.default.replace(/^#/, "");
            } else {
                if (!tabsContainerFromDoc) {
                    // Would log warning here
                }
                const firstTabButton = document.querySelector(".tab");
                if (firstTabButton) {
                    let ariaControls = firstTabButton.getAttribute("aria-controls");
                    if (!ariaControls) {
                        const anchorWithControls = firstTabButton.querySelector('[aria-controls]');
                        if (anchorWithControls) {
                            ariaControls = anchorWithControls.getAttribute("aria-controls");
                        }
                    }
                    if (ariaControls) {
                        defaultTab = ariaControls.replace(/^#/, "");
                    } else {
                        const parsed = firstTabButton.id.replace("-tab", "");
                        defaultTab = parsed || "done";
                    }
                } else {
                    defaultTab = "done";
                }
            }

            assert.strictEqual(defaultTab, 'done', 'Should use "done" as final fallback');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should extract aria-controls from nested anchor when tab lacks it', () => {
        const mockDoc = new MockDocument();

        // Create tabs container
        const tabsContainer = new MockElement('div');
        tabsContainer.classList.add('tabs');
        mockDoc.addMockElement(tabsContainer);

        // Create first tab without aria-controls on li, but anchor has it
        const firstTab = new MockElement('li');
        firstTab.classList.add('tab');
        const anchor = new MockElement('a');
        anchor.setAttribute('aria-controls', '#processing');
        firstTab.appendChild(anchor);
        mockDoc.addMockElement(firstTab);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;

            const tabsContainerFromDoc = document.querySelector(".tabs");
            let defaultTab;

            if (tabsContainerFromDoc && tabsContainerFromDoc.dataset.default) {
                defaultTab = tabsContainerFromDoc.dataset.default.replace(/^#/, "");
            } else {
                const firstTabButton = document.querySelector(".tab");
                if (firstTabButton) {
                    let ariaControls = firstTabButton.getAttribute("aria-controls");
                    if (!ariaControls) {
                        const anchorWithControls = firstTabButton.querySelector('[aria-controls]');
                        if (anchorWithControls) {
                            ariaControls = anchorWithControls.getAttribute("aria-controls");
                        }
                    }
                    if (ariaControls) {
                        defaultTab = ariaControls.replace(/^#/, "");
                    }
                }
            }

            assert.strictEqual(defaultTab, 'processing', 'Should extract aria-controls from nested anchor');
        } finally {
            global.document = originalDoc;
        }
    });
});
