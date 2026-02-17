const { describe, it, before, after } = require('node:test');
const assert = require('node:assert');
const { hideLoadingOverlay, expandFolder, initArrowListeners, initSearch, fuzzyMatch, resetPanel, findParentId } = require('./importer.js');

// Save the original requestAnimationFrame before any reassignment
// Note: requestAnimationFrame is undefined in Node.js by default
const _origRequestAnimationFrame = global.requestAnimationFrame;

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
    }

    set className(value) {
        this._className = value;
        // Sync classList with className
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
        console.warn('Unsupported selector pattern:', selector);
        return false;
    }

    querySelector(selector) {
        if (selector === '.arrow i') {
            for (const child of this.children) {
                if (child.classList && child.classList.contains('arrow')) {
                    for (const subChild of child.children) {
                        if (subChild.tagName === 'i') return subChild;
                    }
                }
            }
            return null;
        }
        if (selector.startsWith('.')) {
            const className = selector.slice(1);
            for (const child of this.children) {
                if (child.classList && child.classList.contains(className)) return child;
            }
        }
        return null;
    }

    querySelectorAll(selector) {
        const results = [];
        if (selector === 'label.panel-block[folder-id=""]') {
            for (const child of this.children) {
                if (child.tagName === 'label' && child.classList && child.classList.contains('panel-block')) {
                    const attr = child.attributes.get('folder-id');
                    if (attr === '') results.push(child);
                }
            }
            return results;
        }
        if (selector === 'label.panel-block:not([folder-id=""])') {
            for (const child of this.children) {
                if (child.tagName === 'label' && child.classList && child.classList.contains('panel-block')) {
                    const attr = child.attributes.get('folder-id');
                    if (attr && attr !== '') results.push(child);
                }
            }
            return results;
        }
        if (selector.includes('[folder-id^=')) {
            const match = selector.match(/folder-id\^='([^']+)'/);
            if (match) {
                const folderId = match[1];
                for (const child of this.children) {
                    if (child.classList && child.classList.contains('panel-block')) {
                        const attr = child.attributes.get('folder-id');
                        if (attr && attr.startsWith(folderId)) results.push(child);
                    }
                }
            }
            return results;
        }
        if (selector === '.arrow i') {
            for (const child of this.children) {
                if (child.classList && child.classList.contains('arrow')) {
                    for (const subChild of child.children) {
                        if (subChild.tagName === 'i') results.push(subChild);
                    }
                }
            }
            return results;
        }
        if (selector === 'label') {
            for (const child of this.children) {
                if (child.tagName === 'label') results.push(child);
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

    getAttribute(name) { return this.attributes.get(name) || null; }
    setAttribute(name, value) { this.attributes.set(name, value); }
}

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
    toggle(className) {
        if (this.classes.has(className)) {
            this.classes.delete(className);
        } else {
            this.classes.add(className);
        }
        if (this.parent) this.parent._className = Array.from(this.classes).join(' ');
        return this.classes.has(className);
    }
}

class MockDocument {
    constructor() {
        this.elements = new Map();
        this._mockElements = [];
    }

    getElementById(id) { return this.elements.get(id) || null; }
    setElement(id, element) { this.elements.set(id, element); }
    addMockElement(element) { this._mockElements.push(element); }

    querySelector(selector) {
        const folderMatch = selector.match(/\.folder\[id\^=['"]([^'"]+)['"]\]/);
        if (folderMatch) {
            const folderId = folderMatch[1];
            for (const el of this._mockElements) {
                if (el.classList.contains('folder')) {
                    const id = el.getAttribute('id');
                    if (id && id.startsWith(folderId)) return el;
                }
            }
            return null;
        }
        if (selector === '.panel-block-container') {
            for (const el of this._mockElements) {
                if (el.classList.contains('panel-block-container')) return el;
            }
            return null;
        }
        if (selector === '.arrow i') {
            for (const el of this._mockElements) {
                if (el.classList.contains('arrow')) {
                    for (const child of el.children) {
                        if (child.tagName === 'i') return child;
                    }
                }
            }
            return null;
        }
        if (selector === '#search-input') {
            return this.getElementById('search-input');
        }
        if (selector === '.clear-search') {
            for (const el of this._mockElements) {
                if (el.classList.contains('clear-search')) return el;
            }
            return null;
        }
        return null;
    }

    querySelectorAll(selector) {
        const results = [];
        if (selector === '.arrow i') {
            for (const el of this._mockElements) {
                if (el.classList.contains('arrow')) {
                    for (const child of el.children) {
                        if (child.tagName === 'i') results.push(child);
                    }
                }
            }
            return results;
        }
        const panelBlockMatch = selector.match(/\.panel-block\[folder-id\^=['"]([^'"]+)['"]\]/);
        if (panelBlockMatch) {
            const folderId = panelBlockMatch[1];
            for (const el of this._mockElements) {
                if (el.classList.contains('panel-block')) {
                    const fid = el.getAttribute('folder-id');
                    if (fid && fid.startsWith(folderId)) results.push(el);
                }
            }
            return results;
        }
        return results;
    }

    createElement(tagName) { return new MockElement(tagName); }
}

// Use before/after hooks to manage the override
before(() => {
    global.requestAnimationFrame = (callback) => setTimeout(callback, 0);
});

after(() => {
    global.requestAnimationFrame = _origRequestAnimationFrame;
});

describe('hideLoadingOverlay', () => {
    it('should set loading-overlay display to none when element exists', () => {
        const mockDoc = new MockDocument();
        const loadingOverlay = new MockElement();
        mockDoc.setElement('loading-overlay', loadingOverlay);
        hideLoadingOverlay(mockDoc);
        assert.strictEqual(loadingOverlay.style.display, 'none');
    });

    it('should not throw error when loading-overlay element does not exist', () => {
        const mockDoc = new MockDocument();
        assert.doesNotThrow(() => hideLoadingOverlay(mockDoc));
    });

    it('should not throw error when element has no style property', () => {
        const mockDoc = new MockDocument();
        mockDoc.setElement('loading-overlay', {});
        assert.doesNotThrow(() => hideLoadingOverlay(mockDoc));
    });
});

describe('expandFolder', () => {
    it('should create and remove spinner element when expanding a collapsed folder', async () => {
        const mockDoc = new MockDocument();
        const folder = new MockElement('div');
        folder.classList.add('folder');
        folder.setAttribute('id', 'folder-test');

        const arrowDiv = new MockElement('div');
        arrowDiv.classList.add('arrow');
        const arrowIcon = new MockElement('i');
        arrowDiv.children.push(arrowIcon);
        folder.children.push(arrowDiv);

        const item = new MockElement('label');
        item.classList.add('panel-block');
        item.setAttribute('folder-id', 'folder-test');
        item.style.display = 'none';

        mockDoc.addMockElement(folder);
        mockDoc.addMockElement(item);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            expandFolder('folder-test');

            // Assert spinner was created immediately
            const spinnerCreated = folder.children.some(child => child.classList && child.classList.contains('folder-loading'));
            assert.strictEqual(spinnerCreated, true, 'Spinner element should be created immediately after expandFolder call');

            // Flush the requestAnimationFrame callback (mocked as setTimeout(callback, 0))
            await new Promise(resolve => setTimeout(resolve, 0));

            // Assert spinner was removed after async operation
            const spinnerRemoved = !folder.children.some(child => child.classList && child.classList.contains('folder-loading'));
            assert.strictEqual(spinnerRemoved, true, 'Spinner element should be removed after requestAnimationFrame callback');

            assert.strictEqual(item.style.display, '', 'Item should be visible after expansion');
            assert.strictEqual(arrowIcon.classList.contains('fa-rotate-90'), true, 'Arrow should have fa-rotate-90 class');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should handle already-expanded folders by collapsing them', () => {
        const mockDoc = new MockDocument();
        const folder = new MockElement('div');
        folder.classList.add('folder');
        folder.setAttribute('id', 'folder-test');

        const arrowDiv = new MockElement('div');
        arrowDiv.classList.add('arrow');
        const arrowIcon = new MockElement('i');
        arrowIcon.classList.add('fa-rotate-90');
        arrowDiv.children.push(arrowIcon);
        folder.children.push(arrowDiv);

        const item = new MockElement('label');
        item.classList.add('panel-block');
        item.setAttribute('folder-id', 'folder-test');

        mockDoc.addMockElement(folder);
        mockDoc.addMockElement(item);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            expandFolder('folder-test');

            assert.strictEqual(item.style.display, 'none', 'Item should be hidden after collapse');
            assert.strictEqual(arrowIcon.classList.contains('fa-rotate-90'), false, 'Arrow should not have fa-rotate-90 class');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should handle missing folder element gracefully', () => {
        const mockDoc = new MockDocument();
        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            assert.doesNotThrow(() => expandFolder('non-existent-folder'));
        } finally {
            global.document = originalDoc;
        }
    });

    it('should handle missing arrow element gracefully', () => {
        const mockDoc = new MockDocument();
        const folder = new MockElement('div');
        folder.classList.add('folder');
        folder.setAttribute('id', 'folder-test');
        mockDoc.addMockElement(folder);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            assert.doesNotThrow(() => expandFolder('folder-test'));
        } finally {
            global.document = originalDoc;
        }
    });
});

describe('initArrowListeners', () => {
    it('should attach click event listeners to arrow elements', () => {
        const mockDoc = new MockDocument();
        const arrowDiv = new MockElement('div');
        arrowDiv.classList.add('arrow');
        const arrowIcon = new MockElement('i');
        arrowIcon.id = 'arrow-1';
        arrowDiv.children.push(arrowIcon);
        mockDoc.addMockElement(arrowDiv);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            initArrowListeners();
        } finally {
            global.document = originalDoc;
        }

        assert.strictEqual(arrowIcon.eventListeners.has('click'), true, 'Click listener should be attached');
    });

    it('should toggle expanded state when arrow is clicked', () => {
        const mockDoc = new MockDocument();
        const folder = new MockElement('div');
        folder.classList.add('folder');
        folder.setAttribute('id', 'folder-test');

        const arrowDiv = new MockElement('div');
        arrowDiv.classList.add('arrow');
        const arrowIcon = new MockElement('i');
        arrowIcon.id = 'folder-test';
        arrowDiv.children.push(arrowIcon);
        folder.children.push(arrowDiv);

        const item = new MockElement('label');
        item.classList.add('panel-block');
        item.setAttribute('folder-id', 'folder-test');
        item.style.display = 'none';

        mockDoc.addMockElement(folder);
        mockDoc.addMockElement(arrowDiv);
        mockDoc.addMockElement(item);

        const originalDoc = global.document;
        try {
            global.document = mockDoc;
            initArrowListeners();

            const clickEvent = { preventDefault: () => {} };
            const handlers = arrowIcon.eventListeners.get('click');
            assert.ok(handlers && handlers.length > 0, 'Click handlers should exist');

            handlers[0](clickEvent);
        } finally {
            global.document = originalDoc;
        }

        assert.strictEqual(item.style.display, '', 'Item should be visible after click');
    });
});

describe('fuzzyMatch', () => {
    it('should return true for exact match', () => {
        assert.strictEqual(fuzzyMatch('hello', 'hello'), true);
        assert.strictEqual(fuzzyMatch('test', 'test'), true);
    });

    it('should return true for fuzzy match with characters in order', () => {
        assert.strictEqual(fuzzyMatch('hl', 'hello'), true);
        assert.strictEqual(fuzzyMatch('tst', 'test'), true);
        assert.strictEqual(fuzzyMatch('abc', 'aabbcc'), true);
        assert.strictEqual(fuzzyMatch('xyz', 'xaybzc'), true);
    });

    it('should return false when needle is not in haystack', () => {
        assert.strictEqual(fuzzyMatch('xyz', 'hello'), false);
        assert.strictEqual(fuzzyMatch('abc', 'def'), false);
        assert.strictEqual(fuzzyMatch('world', 'hello'), false);
    });

    it('should return false when needle is longer than haystack', () => {
        assert.strictEqual(fuzzyMatch('hello', 'hi'), false);
        assert.strictEqual(fuzzyMatch('world', 'wor'), false);
        assert.strictEqual(fuzzyMatch('abcdef', 'abc'), false);
    });

    it('should return false when characters are out of order', () => {
        assert.strictEqual(fuzzyMatch('cba', 'abc'), false);
        assert.strictEqual(fuzzyMatch('olleh', 'hello'), false);
    });

    it('should handle empty strings', () => {
        assert.strictEqual(fuzzyMatch('', 'hello'), true);
        assert.strictEqual(fuzzyMatch('', ''), true);
    });

    it('should handle case sensitivity correctly', () => {
        assert.strictEqual(fuzzyMatch('HELLO', 'hello'), false);
        assert.strictEqual(fuzzyMatch('Hello', 'hello'), false);
    });
});

describe('resetPanel', () => {
    it('should show all depth-0 items', () => {
        const mockDoc = new MockDocument();
        const container = new MockElement('div');
        container.classList.add('panel-block-container');

        const depth0Item = new MockElement('label');
        depth0Item.classList.add('panel-block');
        depth0Item.setAttribute('folder-id', '');
        depth0Item.style.display = 'none';
        container.children.push(depth0Item);

        mockDoc.addMockElement(container);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            resetPanel();
            assert.strictEqual(depth0Item.style.display, '', 'Depth-0 item should be visible');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should hide non-depth-0 items', () => {
        const mockDoc = new MockDocument();
        const container = new MockElement('div');
        container.classList.add('panel-block-container');

        const depthItem = new MockElement('label');
        depthItem.classList.add('panel-block');
        depthItem.setAttribute('folder-id', 'folder-1');
        depthItem.style.display = '';
        container.children.push(depthItem);

        mockDoc.addMockElement(container);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            resetPanel();
            assert.strictEqual(depthItem.style.display, 'none', 'Non-depth-0 item should be hidden');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should remove fa-rotate-90 class from arrows', () => {
        const mockDoc = new MockDocument();
        const container = new MockElement('div');
        container.classList.add('panel-block-container');
        mockDoc.addMockElement(container);

        const arrowDiv = new MockElement('div');
        arrowDiv.classList.add('arrow');
        const arrowIcon = new MockElement('i');
        arrowIcon.classList.add('fa-rotate-90');
        arrowDiv.children.push(arrowIcon);
        mockDoc.addMockElement(arrowDiv);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            resetPanel();
            assert.strictEqual(arrowIcon.classList.contains('fa-rotate-90'), false, 'fa-rotate-90 class should be removed');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should handle missing panel-block-container gracefully', () => {
        const mockDoc = new MockDocument();
        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            assert.doesNotThrow(() => resetPanel());
        } finally {
            global.document = originalDoc;
        }
    });
});

describe('initSearch', () => {
    it('should attach input event listener to search input', () => {
        const mockDoc = new MockDocument();
        const searchInput = new MockElement('input');
        searchInput.setAttribute('id', 'search-input');
        mockDoc.setElement('search-input', searchInput);

        const panelBlock = new MockElement('div');
        panelBlock.classList.add('panel-block-container');
        mockDoc.addMockElement(panelBlock);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            initSearch();
            assert.strictEqual(searchInput.eventListeners.has('input'), true, 'Input listener should be attached');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should update DOM visibility based on fuzzy matching', () => {
        const mockDoc = new MockDocument();
        const searchInput = new MockElement('input');
        searchInput.setAttribute('id', 'search-input');
        searchInput.value = 'test';
        mockDoc.setElement('search-input', searchInput);

        const container = new MockElement('div');
        container.classList.add('panel-block-container');

        const label1 = new MockElement('label');
        label1.textContent = 'test folder';
        container.children.push(label1);

        const label2 = new MockElement('label');
        label2.textContent = 'other folder';
        container.children.push(label2);

        mockDoc.addMockElement(container);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            initSearch();

            const handlers = searchInput.eventListeners.get('input');
            assert.ok(handlers && handlers.length > 0, 'Input handlers should exist');
            handlers[0]();

            assert.strictEqual(label1.style.display, '', 'Matching label should be visible');
            assert.strictEqual(label2.style.display, 'none', 'Non-matching label should be hidden');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should call resetPanel when search query is empty', () => {
        const mockDoc = new MockDocument();
        const searchInput = new MockElement('input');
        searchInput.setAttribute('id', 'search-input');
        searchInput.value = '';
        mockDoc.setElement('search-input', searchInput);

        const container = new MockElement('div');
        container.classList.add('panel-block-container');

        const depth0Item = new MockElement('label');
        depth0Item.classList.add('panel-block');
        depth0Item.setAttribute('folder-id', '');
        depth0Item.style.display = 'none';
        container.children.push(depth0Item);

        const nonDepthItem = new MockElement('label');
        nonDepthItem.classList.add('panel-block');
        nonDepthItem.setAttribute('folder-id', 'folder-1');
        nonDepthItem.style.display = '';
        container.children.push(nonDepthItem);

        mockDoc.addMockElement(container);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            initSearch();

            const handlers = searchInput.eventListeners.get('input');
            assert.ok(handlers && handlers.length > 0, 'Input handlers should exist');
            handlers[0]();

            assert.strictEqual(depth0Item.style.display, '', 'resetPanel should show depth-0 items');
            assert.strictEqual(nonDepthItem.style.display, 'none', 'resetPanel should hide non-depth-0 items');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should attach clear search button listener if present', () => {
        const mockDoc = new MockDocument();
        const searchInput = new MockElement('input');
        searchInput.setAttribute('id', 'search-input');
        mockDoc.setElement('search-input', searchInput);

        const container = new MockElement('div');
        container.classList.add('panel-block-container');
        mockDoc.addMockElement(container);

        const clearButton = new MockElement('button');
        clearButton.classList.add('clear-search');
        mockDoc.addMockElement(clearButton);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            initSearch();
            assert.strictEqual(clearButton.eventListeners.has('click'), true, 'Clear button should have click listener');
        } finally {
            global.document = originalDoc;
        }
    });

    it('should handle missing search input gracefully', () => {
        const mockDoc = new MockDocument();
        const container = new MockElement('div');
        container.classList.add('panel-block-container');
        mockDoc.addMockElement(container);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            assert.doesNotThrow(() => initSearch());
        } finally {
            global.document = originalDoc;
        }
    });

    it('should handle missing panel-block-container gracefully', () => {
        const mockDoc = new MockDocument();
        const searchInput = new MockElement('input');
        searchInput.setAttribute('id', 'search-input');
        mockDoc.setElement('search-input', searchInput);

        const originalDoc = global.document;
        global.document = mockDoc;
        try {
            assert.doesNotThrow(() => initSearch());
        } finally {
            global.document = originalDoc;
        }
    });
});

describe('findParentId', () => {
    it('should return empty string for root-level paths', () => {
        const parentMap = new Map();
        assert.strictEqual(findParentId('file.txt', 0, parentMap), '');
        assert.strictEqual(findParentId('folder', 0, parentMap), '');
    });

    it('should return parent path for nested paths', () => {
        const parentMap = new Map();
        parentMap.set('parent', 'parent-id');
        assert.strictEqual(findParentId('parent/child', 1, parentMap), 'parent-id');
    });

    it('should handle Unix-style paths correctly', () => {
        const parentMap = new Map();
        parentMap.set('dir1/dir2', 'dir2-id');
        assert.strictEqual(findParentId('dir1/dir2/file.txt', 2, parentMap), 'dir2-id');
    });

    it('should handle Windows-style paths correctly', () => {
        const parentMap = new Map();
        parentMap.set('dir1/dir2', 'dir2-id');
        // Windows paths with backslashes
        assert.strictEqual(findParentId('dir1\\dir2\\file.txt', 2, parentMap), 'dir2-id');
    });

    it('should handle mixed path separators correctly', () => {
        const parentMap = new Map();
        parentMap.set('dir1/dir2', 'dir2-id');
        // Mixed separators
        assert.strictEqual(findParentId('dir1\\dir2/file.txt', 2, parentMap), 'dir2-id');
    });

    it('should return empty string when parent not in map', () => {
        const parentMap = new Map();
        assert.strictEqual(findParentId('nonexistent/child', 1, parentMap), '');
    });

    it('should handle empty parentMap', () => {
        const parentMap = new Map();
        assert.strictEqual(findParentId('path/to/file.txt', 2, parentMap), '');
    });

    it('should handle paths with only one segment', () => {
        const parentMap = new Map();
        assert.strictEqual(findParentId('single', 0, parentMap), '');
    });

    it('should handle Windows root-level paths', () => {
        const parentMap = new Map();
        assert.strictEqual(findParentId('C:\\folder\\file.txt', 0, parentMap), '');
    });
});
