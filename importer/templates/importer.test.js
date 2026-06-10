const { describe, it, before, after } = require("node:test");
const assert = require("node:assert");
const {
  hideLoadingOverlay,
  expandFolder,
  initArrowListeners,
  initSearch,
  fuzzyMatch,
  resetPanel,
  findParentId,
} = require("./importer.js");
const { MockElement, MockClassList, MockDocument } = require("./test_helpers");

// Save original before overriding
const _origRequestAnimationFrame = global.requestAnimationFrame;

before(() => {
  global.requestAnimationFrame = (callback) => setTimeout(callback, 0);
});

after(() => {
  global.requestAnimationFrame = _origRequestAnimationFrame;
});

describe("hideLoadingOverlay", () => {
  it("should add hidden class to pre-loader when element exists", () => {
    const mockDoc = new MockDocument();
    const preLoader = new MockElement();
    mockDoc.setElement("pre-loader", preLoader);
    hideLoadingOverlay(mockDoc);
    assert.strictEqual(preLoader.classList.contains("hidden"), true);
  });

  it("should not throw error when pre-loader element does not exist", () => {
    const mockDoc = new MockDocument();
    assert.doesNotThrow(() => hideLoadingOverlay(mockDoc));
  });

  it("should not throw error when pre-loader element lacks parentNode", () => {
    const mockDoc = new MockDocument();
    const preLoader = new MockElement();
    mockDoc.setElement("pre-loader", preLoader);
    assert.doesNotThrow(() => hideLoadingOverlay(mockDoc));
  });
});

describe("expandFolder", () => {
  it("should create and remove spinner element when expanding a collapsed folder", async () => {
    const mockDoc = new MockDocument();
    const folder = new MockElement("div");
    folder.classList.add("folder");
    folder.setAttribute("id", "folder-test");

    const arrowDiv = new MockElement("div");
    arrowDiv.classList.add("arrow");
    const arrowIcon = new MockElement("i");
    arrowDiv.children.push(arrowIcon);
    folder.children.push(arrowDiv);

    const item = new MockElement("label");
    item.classList.add("panel-block");
    item.setAttribute("folder-id", "folder-test");
    item.style.display = "none";

    mockDoc.addMockElement(folder);
    mockDoc.addMockElement(item);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      expandFolder("folder-test");

      // Assert spinner was created immediately
      const spinnerCreated = folder.children.some(
        (child) =>
          child.classList && child.classList.contains("folder-loading"),
      );
      assert.strictEqual(
        spinnerCreated,
        true,
        "Spinner element should be created immediately after expandFolder call",
      );

      // Flush the requestAnimationFrame callback (mocked as setTimeout(callback, 0))
      await new Promise((resolve) => setTimeout(resolve, 0));

      // Assert spinner was removed after async operation
      const spinnerRemoved = !folder.children.some(
        (child) =>
          child.classList && child.classList.contains("folder-loading"),
      );
      assert.strictEqual(
        spinnerRemoved,
        true,
        "Spinner element should be removed after requestAnimationFrame callback",
      );

      assert.strictEqual(
        item.style.display,
        "",
        "Item should be visible after expansion",
      );
      assert.strictEqual(
        arrowIcon.classList.contains("fa-rotate-90"),
        true,
        "Arrow should have fa-rotate-90 class",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should handle already-expanded folders by collapsing them", () => {
    const mockDoc = new MockDocument();
    const folder = new MockElement("div");
    folder.classList.add("folder");
    folder.setAttribute("id", "folder-test");

    const arrowDiv = new MockElement("div");
    arrowDiv.classList.add("arrow");
    const arrowIcon = new MockElement("i");
    arrowIcon.classList.add("fa-rotate-90");
    arrowDiv.children.push(arrowIcon);
    folder.children.push(arrowDiv);

    const item = new MockElement("label");
    item.classList.add("panel-block");
    item.setAttribute("folder-id", "folder-test");

    mockDoc.addMockElement(folder);
    mockDoc.addMockElement(item);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      expandFolder("folder-test");

      assert.strictEqual(
        item.style.display,
        "none",
        "Item should be hidden after collapse",
      );
      assert.strictEqual(
        arrowIcon.classList.contains("fa-rotate-90"),
        false,
        "Arrow should not have fa-rotate-90 class",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should handle missing folder element gracefully", () => {
    const mockDoc = new MockDocument();
    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      assert.doesNotThrow(() => expandFolder("non-existent-folder"));
    } finally {
      global.document = originalDoc;
    }
  });

  it("should handle missing arrow element gracefully", () => {
    const mockDoc = new MockDocument();
    const folder = new MockElement("div");
    folder.classList.add("folder");
    folder.setAttribute("id", "folder-test");
    mockDoc.addMockElement(folder);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      assert.doesNotThrow(() => expandFolder("folder-test"));
    } finally {
      global.document = originalDoc;
    }
  });
});

describe("initArrowListeners", () => {
  it("should attach click event listeners to arrow elements", () => {
    const mockDoc = new MockDocument();
    const arrowDiv = new MockElement("div");
    arrowDiv.classList.add("arrow");
    const arrowIcon = new MockElement("i");
    arrowIcon.id = "arrow-1";
    arrowDiv.children.push(arrowIcon);
    mockDoc.addMockElement(arrowDiv);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      initArrowListeners();
    } finally {
      global.document = originalDoc;
    }

    assert.strictEqual(
      arrowIcon.eventListeners.has("click"),
      true,
      "Click listener should be attached",
    );
  });

  it("should toggle expanded state when arrow is clicked", () => {
    const mockDoc = new MockDocument();
    const folder = new MockElement("div");
    folder.classList.add("folder");
    folder.setAttribute("id", "folder-test");

    const arrowDiv = new MockElement("div");
    arrowDiv.classList.add("arrow");
    const arrowIcon = new MockElement("i");
    arrowIcon.id = "folder-test";
    arrowDiv.children.push(arrowIcon);
    folder.children.push(arrowDiv);

    const item = new MockElement("label");
    item.classList.add("panel-block");
    item.setAttribute("folder-id", "folder-test");
    item.style.display = "none";

    mockDoc.addMockElement(folder);
    mockDoc.addMockElement(arrowDiv);
    mockDoc.addMockElement(item);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      initArrowListeners();

      const clickEvent = { preventDefault: () => {} };
      const handlers = arrowIcon.eventListeners.get("click");
      assert.ok(handlers && handlers.length > 0, "Click handlers should exist");

      handlers[0](clickEvent);
    } finally {
      global.document = originalDoc;
    }

    assert.strictEqual(
      item.style.display,
      "",
      "Item should be visible after click",
    );
  });
});

describe("fuzzyMatch", () => {
  it("should return true for exact match", () => {
    assert.strictEqual(fuzzyMatch("hello", "hello"), true);
    assert.strictEqual(fuzzyMatch("test", "test"), true);
  });

  it("should return true for fuzzy match with characters in order", () => {
    assert.strictEqual(fuzzyMatch("hl", "hello"), true);
    assert.strictEqual(fuzzyMatch("tst", "test"), true);
    assert.strictEqual(fuzzyMatch("abc", "aabbcc"), true);
    assert.strictEqual(fuzzyMatch("xyz", "xaybzc"), true);
  });

  it("should return false when needle is not in haystack", () => {
    assert.strictEqual(fuzzyMatch("xyz", "hello"), false);
    assert.strictEqual(fuzzyMatch("abc", "def"), false);
    assert.strictEqual(fuzzyMatch("world", "hello"), false);
  });

  it("should return false when needle is longer than haystack", () => {
    assert.strictEqual(fuzzyMatch("hello", "hi"), false);
    assert.strictEqual(fuzzyMatch("world", "wor"), false);
    assert.strictEqual(fuzzyMatch("abcdef", "abc"), false);
  });

  it("should return false when characters are out of order", () => {
    assert.strictEqual(fuzzyMatch("cba", "abc"), false);
    assert.strictEqual(fuzzyMatch("olleh", "hello"), false);
  });

  it("should handle empty strings", () => {
    assert.strictEqual(fuzzyMatch("", "hello"), true);
    assert.strictEqual(fuzzyMatch("", ""), true);
  });

  it("should handle case sensitivity correctly", () => {
    assert.strictEqual(fuzzyMatch("HELLO", "hello"), false);
    assert.strictEqual(fuzzyMatch("Hello", "hello"), false);
  });
});

describe("resetPanel", () => {
  it("should show all depth-0 items", () => {
    const mockDoc = new MockDocument();
    const container = new MockElement("div");
    container.classList.add("panel-block-container");

    const depth0Item = new MockElement("label");
    depth0Item.classList.add("panel-block");
    depth0Item.setAttribute("folder-id", "");
    depth0Item.style.display = "none";
    container.children.push(depth0Item);

    mockDoc.addMockElement(container);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      resetPanel();
      assert.strictEqual(
        depth0Item.style.display,
        "",
        "Depth-0 item should be visible",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should hide non-depth-0 items", () => {
    const mockDoc = new MockDocument();
    const container = new MockElement("div");
    container.classList.add("panel-block-container");

    const depthItem = new MockElement("label");
    depthItem.classList.add("panel-block");
    depthItem.setAttribute("folder-id", "folder-1");
    depthItem.style.display = "";
    container.children.push(depthItem);

    mockDoc.addMockElement(container);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      resetPanel();
      assert.strictEqual(
        depthItem.style.display,
        "none",
        "Non-depth-0 item should be hidden",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should remove fa-rotate-90 class from arrows", () => {
    const mockDoc = new MockDocument();
    const container = new MockElement("div");
    container.classList.add("panel-block-container");
    mockDoc.addMockElement(container);

    const arrowDiv = new MockElement("div");
    arrowDiv.classList.add("arrow");
    const arrowIcon = new MockElement("i");
    arrowIcon.classList.add("fa-rotate-90");
    arrowDiv.children.push(arrowIcon);
    mockDoc.addMockElement(arrowDiv);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      resetPanel();
      assert.strictEqual(
        arrowIcon.classList.contains("fa-rotate-90"),
        false,
        "fa-rotate-90 class should be removed",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should handle missing panel-block-container gracefully", () => {
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

describe("initSearch", () => {
  it("should attach input event listener to search input", () => {
    const mockDoc = new MockDocument();
    const searchInput = new MockElement("input");
    searchInput.setAttribute("id", "search-input");
    mockDoc.setElement("search-input", searchInput);

    const panelBlock = new MockElement("div");
    panelBlock.classList.add("panel-block-container");
    mockDoc.addMockElement(panelBlock);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      initSearch();
      assert.strictEqual(
        searchInput.eventListeners.has("input"),
        true,
        "Input listener should be attached",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should update DOM visibility based on fuzzy matching", () => {
    const mockDoc = new MockDocument();
    const searchInput = new MockElement("input");
    searchInput.setAttribute("id", "search-input");
    searchInput.value = "test";
    mockDoc.setElement("search-input", searchInput);

    const container = new MockElement("div");
    container.classList.add("panel-block-container");

    const label1 = new MockElement("label");
    label1.textContent = "test folder";
    container.children.push(label1);

    const label2 = new MockElement("label");
    label2.textContent = "other folder";
    container.children.push(label2);

    mockDoc.addMockElement(container);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      initSearch();

      const handlers = searchInput.eventListeners.get("input");
      assert.ok(handlers && handlers.length > 0, "Input handlers should exist");
      handlers[0]();

      assert.strictEqual(
        label1.style.display,
        "",
        "Matching label should be visible",
      );
      assert.strictEqual(
        label2.style.display,
        "none",
        "Non-matching label should be hidden",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should call resetPanel when search query is empty", () => {
    const mockDoc = new MockDocument();
    const searchInput = new MockElement("input");
    searchInput.setAttribute("id", "search-input");
    searchInput.value = "";
    mockDoc.setElement("search-input", searchInput);

    const container = new MockElement("div");
    container.classList.add("panel-block-container");

    const depth0Item = new MockElement("label");
    depth0Item.classList.add("panel-block");
    depth0Item.setAttribute("folder-id", "");
    depth0Item.style.display = "none";
    container.children.push(depth0Item);

    const nonDepthItem = new MockElement("label");
    nonDepthItem.classList.add("panel-block");
    nonDepthItem.setAttribute("folder-id", "folder-1");
    nonDepthItem.style.display = "";
    container.children.push(nonDepthItem);

    mockDoc.addMockElement(container);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      initSearch();

      const handlers = searchInput.eventListeners.get("input");
      assert.ok(handlers && handlers.length > 0, "Input handlers should exist");
      handlers[0]();

      assert.strictEqual(
        depth0Item.style.display,
        "",
        "resetPanel should show depth-0 items",
      );
      assert.strictEqual(
        nonDepthItem.style.display,
        "none",
        "resetPanel should hide non-depth-0 items",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should attach clear search button listener if present", () => {
    const mockDoc = new MockDocument();
    const searchInput = new MockElement("input");
    searchInput.setAttribute("id", "search-input");
    mockDoc.setElement("search-input", searchInput);

    const container = new MockElement("div");
    container.classList.add("panel-block-container");
    mockDoc.addMockElement(container);

    const clearButton = new MockElement("button");
    clearButton.classList.add("clear-search");
    mockDoc.addMockElement(clearButton);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      initSearch();
      assert.strictEqual(
        clearButton.eventListeners.has("click"),
        true,
        "Clear button should have click listener",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should handle missing search input gracefully", () => {
    const mockDoc = new MockDocument();
    const container = new MockElement("div");
    container.classList.add("panel-block-container");
    mockDoc.addMockElement(container);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      assert.doesNotThrow(() => initSearch());
    } finally {
      global.document = originalDoc;
    }
  });

  it("should handle missing panel-block-container gracefully", () => {
    const mockDoc = new MockDocument();
    const searchInput = new MockElement("input");
    searchInput.setAttribute("id", "search-input");
    mockDoc.setElement("search-input", searchInput);

    const originalDoc = global.document;
    global.document = mockDoc;
    try {
      assert.doesNotThrow(() => initSearch());
    } finally {
      global.document = originalDoc;
    }
  });
});

describe("findParentId", () => {
  it("should return empty string for root-level paths", () => {
    const parentMap = new Map();
    assert.strictEqual(findParentId("file.txt", 0, parentMap), "");
    assert.strictEqual(findParentId("folder", 0, parentMap), "");
  });

  it("should return parent path for nested paths", () => {
    const parentMap = new Map();
    parentMap.set("parent", "parent-id");
    assert.strictEqual(findParentId("parent/child", 1, parentMap), "parent-id");
  });

  it("should handle Unix-style paths correctly", () => {
    const parentMap = new Map();
    parentMap.set("dir1/dir2", "dir2-id");
    assert.strictEqual(
      findParentId("dir1/dir2/file.txt", 2, parentMap),
      "dir2-id",
    );
  });

  it("should handle Windows-style paths correctly", () => {
    const parentMap = new Map();
    parentMap.set("dir1/dir2", "dir2-id");
    // Windows paths with backslashes
    assert.strictEqual(
      findParentId("dir1\\dir2\\file.txt", 2, parentMap),
      "dir2-id",
    );
  });

  it("should handle mixed path separators correctly", () => {
    const parentMap = new Map();
    parentMap.set("dir1/dir2", "dir2-id");
    // Mixed separators
    assert.strictEqual(
      findParentId("dir1\\dir2/file.txt", 2, parentMap),
      "dir2-id",
    );
  });

  it("should return empty string when parent not in map", () => {
    const parentMap = new Map();
    assert.strictEqual(findParentId("nonexistent/child", 1, parentMap), "");
  });

  it("should handle empty parentMap", () => {
    const parentMap = new Map();
    assert.strictEqual(findParentId("path/to/file.txt", 2, parentMap), "");
  });

  it("should handle paths with only one segment", () => {
    const parentMap = new Map();
    assert.strictEqual(findParentId("single", 0, parentMap), "");
  });

  it("should handle Windows root-level paths", () => {
    const parentMap = new Map();
    assert.strictEqual(findParentId("C:\\folder\\file.txt", 0, parentMap), "");
  });
});
