const { describe, it } = require('node:test');
const assert = require('node:assert');

// Mock DOM implementation
class MockElement {
    constructor() {
        this.style = {};
    }
}

class MockDocument {
    constructor() {
        this.elements = new Map();
    }

    getElementById(id) {
        return this.elements.get(id) || null;
    }

    setElement(id, element) {
        this.elements.set(id, element);
    }
}

describe('hideLoadingOverlay', () => {
    it('should set loading-overlay display to none when element exists', () => {
        const { hideLoadingOverlay } = require('./importer.js');
        const mockDoc = new MockDocument();
        const loadingOverlay = new MockElement();
        mockDoc.setElement('loading-overlay', loadingOverlay);

        hideLoadingOverlay(mockDoc);

        assert.strictEqual(loadingOverlay.style.display, 'none');
    });

    it('should not throw error when loading-overlay element does not exist', () => {
        const { hideLoadingOverlay } = require('./importer.js');
        const mockDoc = new MockDocument();

        assert.doesNotThrow(() => {
            hideLoadingOverlay(mockDoc);
        });
    });

    it('should not throw error when element has no style property', () => {
        const { hideLoadingOverlay } = require('./importer.js');
        const mockDoc = new MockDocument();
        const loadingOverlay = {};
        mockDoc.setElement('loading-overlay', loadingOverlay);

        assert.doesNotThrow(() => {
            hideLoadingOverlay(mockDoc);
        });
    });
});
