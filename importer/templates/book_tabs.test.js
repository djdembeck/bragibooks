const { describe, it, before, after } = require("node:test");
const assert = require("node:assert");
const { openTab, handleKeyDown, initializeTabs } = require("./book_tabs.js");
const {
  MockElement,
  MockClassList,
  MockDocument,
  buildQuerySelectorAllResults,
} = require("./test_helpers");
// Helper function to build a 3-tab mock document for handleKeyDown tests
function build3TabMockDoc(options = {}) {
  const { activeTabIndex = 1 } = options; // 0=done, 1=processing, 2=error

  const mockDoc = new MockDocument();

  // Create first tab (done)
  const tab1 = new MockElement("li");
  tab1.classList.add("tab");
  const anchor1 = new MockElement("a");
  anchor1.setAttribute("role", "tab");
  anchor1.setAttribute("aria-controls", "done");
  tab1.appendChild(anchor1);
  const donePane = new MockElement("div");
  donePane.classList.add("tab-pane");
  mockDoc.setElement("done", donePane);
  const doneButton = new MockElement("li");
  doneButton.classList.add("tab");
  mockDoc.setElement("done-tab", doneButton);

  // Create second tab (processing)
  const tab2 = new MockElement("li");
  tab2.classList.add("tab");
  const anchor2 = new MockElement("a");
  anchor2.setAttribute("role", "tab");
  anchor2.setAttribute("aria-controls", "processing");
  tab2.appendChild(anchor2);
  const processingPane = new MockElement("div");
  processingPane.classList.add("tab-pane");
  mockDoc.setElement("processing", processingPane);
  const processingButton = new MockElement("li");
  processingButton.classList.add("tab");
  mockDoc.setElement("processing-tab", processingButton);

  // Create third tab (error)
  const tab3 = new MockElement("li");
  tab3.classList.add("tab");
  const anchor3 = new MockElement("a");
  anchor3.setAttribute("role", "tab");
  anchor3.setAttribute("aria-controls", "error");
  tab3.appendChild(anchor3);
  const errorPane = new MockElement("div");
  errorPane.classList.add("tab-pane");
  mockDoc.setElement("error", errorPane);
  const errorButton = new MockElement("li");
  errorButton.classList.add("tab");
  mockDoc.setElement("error-tab", errorButton);

  mockDoc.addMockElement(tab1);
  mockDoc.addMockElement(tab2);
  mockDoc.addMockElement(tab3);

  // Set active element based on index
  const anchors = [anchor1, anchor2, anchor3];
  mockDoc._activeElement = anchors[activeTabIndex];

  return {
    mockDoc,
    done: { pane: donePane, button: doneButton, anchor: anchor1, tab: tab1 },
    processing: {
      pane: processingPane,
      button: processingButton,
      anchor: anchor2,
      tab: tab2,
    },
    error: { pane: errorPane, button: errorButton, anchor: anchor3, tab: tab3 },
  };
}

describe("openTab", () => {
  it("should show tab element and activate tab button when both exist", () => {
    const mockDoc = new MockDocument();

    // Create tab pane
    const tabPane = new MockElement("div");
    tabPane.classList.add("tab-pane");
    tabPane.style.display = "none";
    mockDoc.setElement("done", tabPane);

    // Create tab button
    const tabButton = new MockElement("li");
    tabButton.classList.add("tab");
    mockDoc.setElement("done-tab", tabButton);

    // Create tab anchor
    const tabAnchor = new MockElement("a");
    tabAnchor.setAttribute("role", "tab");
    tabAnchor.setAttribute("aria-controls", "done");
    tabButton.appendChild(tabAnchor);
    mockDoc.addMockElement(tabButton);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      openTab({ preventDefault: () => {} }, "done", false);

      assert.strictEqual(
        tabPane.style.display,
        "block",
        "Tab pane should be visible",
      );
      assert.strictEqual(
        tabButton.classList.contains("is-active"),
        true,
        "Tab button should have is-active class",
      );
      assert.strictEqual(
        tabAnchor.getAttribute("aria-selected"),
        "true",
        'Anchor should have aria-selected="true"',
      );
      assert.strictEqual(
        tabAnchor.getAttribute("tabindex"),
        "0",
        'Anchor should have tabindex="0"',
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should not modify UI and return early when tab element is missing", () => {
    const mockDoc = new MockDocument();
    const warnings = [];
    const originalWarn = console.warn;
    console.warn = (msg) => warnings.push(msg);

    // Create another tab that is currently active
    const otherPane = new MockElement("div");
    otherPane.classList.add("tab-pane");
    otherPane.style.display = "block";
    mockDoc.setElement("processing", otherPane);

    const otherButton = new MockElement("li");
    otherButton.classList.add("tab");
    otherButton.classList.add("is-active");
    mockDoc.setElement("processing-tab", otherButton);

    mockDoc.addMockElement(otherButton);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      openTab({ preventDefault: () => {} }, "nonexistent", false);

      // The existing active tab should remain unchanged
      assert.strictEqual(
        otherPane.style.display,
        "block",
        "Existing tab pane should remain visible",
      );
      assert.strictEqual(
        otherButton.classList.contains("is-active"),
        true,
        "Existing tab button should remain active",
      );
      assert.strictEqual(warnings.length, 1, "Should log one warning");
      assert.ok(
        warnings[0].includes("'nonexistent' not found"),
        "Warning should mention missing tab element",
      );
    } finally {
      global.document = originalDoc;
      console.warn = originalWarn;
    }
  });

  it("should not modify UI and return early when tab button is missing", () => {
    const mockDoc = new MockDocument();
    const warnings = [];
    const originalWarn = console.warn;
    console.warn = (msg) => warnings.push(msg);

    // Create tab pane but NOT the tab button
    const tabPane = new MockElement("div");
    tabPane.classList.add("tab-pane");
    tabPane.style.display = "none";
    mockDoc.setElement("done", tabPane);

    // Create another tab that is currently active
    const otherPane = new MockElement("div");
    otherPane.classList.add("tab-pane");
    otherPane.style.display = "block";
    mockDoc.setElement("processing", otherPane);

    const otherButton = new MockElement("li");
    otherButton.classList.add("tab");
    otherButton.classList.add("is-active");
    mockDoc.setElement("processing-tab", otherButton);

    mockDoc.addMockElement(otherButton);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      openTab({ preventDefault: () => {} }, "done", false);

      // The existing active tab should remain unchanged since button is missing
      assert.strictEqual(
        otherPane.style.display,
        "block",
        "Existing tab pane should remain visible",
      );
      assert.strictEqual(
        otherButton.classList.contains("is-active"),
        true,
        "Existing tab button should remain active",
      );
      assert.strictEqual(warnings.length, 1, "Should log one warning");
      assert.ok(
        warnings[0].includes("'done-tab' not found"),
        "Warning should mention missing tab button",
      );
    } finally {
      global.document = originalDoc;
      console.warn = originalWarn;
    }
  });

  it("should focus anchor when userInitiated is true", () => {
    const mockDoc = new MockDocument();

    const tabPane = new MockElement("div");
    tabPane.classList.add("tab-pane");
    mockDoc.setElement("done", tabPane);

    const tabButton = new MockElement("li");
    tabButton.classList.add("tab");
    mockDoc.setElement("done-tab", tabButton);

    const tabAnchor = new MockElement("a");
    tabAnchor.setAttribute("role", "tab");
    tabAnchor.setAttribute("aria-controls", "done");
    tabButton.appendChild(tabAnchor);
    mockDoc.addMockElement(tabButton);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;
      openTab({ preventDefault: () => {} }, "done", true);

      assert.strictEqual(
        tabAnchor._focused,
        true,
        "Anchor should be focused when userInitiated is true",
      );
    } finally {
      global.document = originalDoc;
    }
  });
});

describe("handleKeyDown", () => {
  it("should navigate to previous tab with ArrowLeft", () => {
    const { mockDoc, done } = build3TabMockDoc({ activeTabIndex: 1 }); // processing is focused

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowLeft", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify that the "done" tab is now active
      assert.strictEqual(
        done.pane.style.display,
        "block",
        "Done pane should be visible",
      );
      assert.strictEqual(
        done.button.classList.contains("is-active"),
        true,
        "Done button should be active",
      );
      assert.strictEqual(
        done.anchor.getAttribute("aria-selected"),
        "true",
        'Done anchor should have aria-selected="true"',
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should wrap around to last tab with ArrowLeft from first tab", () => {
    const { mockDoc, error } = build3TabMockDoc({ activeTabIndex: 0 }); // done is focused

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowLeft", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify wrap-around to last tab
      assert.strictEqual(
        error.pane.style.display,
        "block",
        "Error pane should be visible (wrap-around)",
      );
      assert.strictEqual(
        error.button.classList.contains("is-active"),
        true,
        "Error button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should navigate to next tab with ArrowRight", () => {
    const { mockDoc, processing } = build3TabMockDoc({ activeTabIndex: 0 }); // done is focused

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowRight", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify navigation to next tab
      assert.strictEqual(
        processing.pane.style.display,
        "block",
        "Processing pane should be visible",
      );
      assert.strictEqual(
        processing.button.classList.contains("is-active"),
        true,
        "Processing button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should wrap around to first tab with ArrowRight from last tab", () => {
    const { mockDoc, done } = build3TabMockDoc({ activeTabIndex: 2 }); // error (last tab) is focused

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowRight", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify wrap-around to first tab
      assert.strictEqual(
        done.pane.style.display,
        "block",
        "Done pane should be visible (wrap-around)",
      );
      assert.strictEqual(
        done.button.classList.contains("is-active"),
        true,
        "Done button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should navigate to first tab with Home key", () => {
    const mockDoc = new MockDocument();

    // Create tabs with panes and buttons
    const tab1 = new MockElement("li");
    tab1.classList.add("tab");
    const anchor1 = new MockElement("a");
    anchor1.setAttribute("role", "tab");
    anchor1.setAttribute("aria-controls", "done");
    tab1.appendChild(anchor1);
    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    mockDoc.setElement("done", donePane);
    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const tab2 = new MockElement("li");
    tab2.classList.add("tab");
    const anchor2 = new MockElement("a");
    anchor2.setAttribute("role", "tab");
    anchor2.setAttribute("aria-controls", "processing");
    tab2.appendChild(anchor2);
    const processingPane = new MockElement("div");
    processingPane.classList.add("tab-pane");
    mockDoc.setElement("processing", processingPane);
    const processingButton = new MockElement("li");
    processingButton.classList.add("tab");
    mockDoc.setElement("processing-tab", processingButton);

    const tab3 = new MockElement("li");
    tab3.classList.add("tab");
    const anchor3 = new MockElement("a");
    anchor3.setAttribute("role", "tab");
    anchor3.setAttribute("aria-controls", "error");
    tab3.appendChild(anchor3);
    const errorPane = new MockElement("div");
    errorPane.classList.add("tab-pane");
    mockDoc.setElement("error", errorPane);
    const errorButton = new MockElement("li");
    errorButton.classList.add("tab");
    mockDoc.setElement("error-tab", errorButton);

    mockDoc.addMockElement(tab1);
    mockDoc.addMockElement(tab2);
    mockDoc.addMockElement(tab3);
    mockDoc._activeElement = anchor3; // Last tab is focused

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "Home", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify navigation to first tab
      assert.strictEqual(
        donePane.style.display,
        "block",
        "Done pane should be visible",
      );
      assert.strictEqual(
        doneButton.classList.contains("is-active"),
        true,
        "Done button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should navigate to last tab with End key", () => {
    const mockDoc = new MockDocument();

    // Create tabs with panes and buttons
    const tab1 = new MockElement("li");
    tab1.classList.add("tab");
    const anchor1 = new MockElement("a");
    anchor1.setAttribute("role", "tab");
    anchor1.setAttribute("aria-controls", "done");
    tab1.appendChild(anchor1);
    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    mockDoc.setElement("done", donePane);
    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const tab2 = new MockElement("li");
    tab2.classList.add("tab");
    const anchor2 = new MockElement("a");
    anchor2.setAttribute("role", "tab");
    anchor2.setAttribute("aria-controls", "processing");
    tab2.appendChild(anchor2);
    const processingPane = new MockElement("div");
    processingPane.classList.add("tab-pane");
    mockDoc.setElement("processing", processingPane);
    const processingButton = new MockElement("li");
    processingButton.classList.add("tab");
    mockDoc.setElement("processing-tab", processingButton);

    const tab3 = new MockElement("li");
    tab3.classList.add("tab");
    const anchor3 = new MockElement("a");
    anchor3.setAttribute("role", "tab");
    anchor3.setAttribute("aria-controls", "error");
    tab3.appendChild(anchor3);
    const errorPane = new MockElement("div");
    errorPane.classList.add("tab-pane");
    mockDoc.setElement("error", errorPane);
    const errorButton = new MockElement("li");
    errorButton.classList.add("tab");
    mockDoc.setElement("error-tab", errorButton);

    mockDoc.addMockElement(tab1);
    mockDoc.addMockElement(tab2);
    mockDoc.addMockElement(tab3);
    mockDoc._activeElement = anchor1; // First tab is focused

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "End", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify navigation to last tab
      assert.strictEqual(
        errorPane.style.display,
        "block",
        "Error pane should be visible",
      );
      assert.strictEqual(
        errorButton.classList.contains("is-active"),
        true,
        "Error button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should not call openTab for unhandled keys", () => {
    const mockDoc = new MockDocument();

    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    donePane.style.display = "none";
    mockDoc.setElement("done", donePane);

    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const tab1 = new MockElement("li");
    tab1.classList.add("tab");
    const anchor1 = new MockElement("a");
    anchor1.setAttribute("role", "tab");
    anchor1.setAttribute("aria-controls", "done");
    tab1.appendChild(anchor1);

    mockDoc.addMockElement(tab1);
    mockDoc._activeElement = anchor1;

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "Enter", preventDefault: () => {} };
      handleKeyDown(event);

      assert.strictEqual(
        donePane.style.display,
        "none",
        "Tab pane should remain hidden for unhandled keys",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should return early when active element is not a tab anchor", () => {
    const mockDoc = new MockDocument();

    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    donePane.style.display = "none";
    mockDoc.setElement("done", donePane);

    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const tab1 = new MockElement("li");
    tab1.classList.add("tab");
    const anchor1 = new MockElement("a");
    anchor1.setAttribute("role", "tab");
    anchor1.setAttribute("aria-controls", "done");
    tab1.appendChild(anchor1);

    mockDoc.addMockElement(tab1);
    mockDoc._activeElement = new MockElement("div");

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowRight", preventDefault: () => {} };
      handleKeyDown(event);

      assert.strictEqual(
        donePane.style.display,
        "none",
        "Tab pane should remain hidden when active element is not a tab anchor",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should log warning when anchor has no aria-controls attribute", () => {
    const mockDoc = new MockDocument();
    const warnings = [];
    const originalWarn = console.warn;
    console.warn = (msg) => warnings.push(msg);

    const tab1 = new MockElement("li");
    tab1.classList.add("tab");
    const anchor1 = new MockElement("a");
    anchor1.setAttribute("role", "tab");
    tab1.appendChild(anchor1);

    mockDoc.addMockElement(tab1);
    mockDoc._activeElement = anchor1;

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowRight", preventDefault: () => {} };
      handleKeyDown(event);

      assert.strictEqual(warnings.length, 1, "Should log one warning");
      assert.ok(
        warnings[0].includes("missing aria-controls attribute"),
        "Warning should mention missing aria-controls",
      );
    } finally {
      global.document = originalDoc;
      console.warn = originalWarn;
    }
  });

  it("should extract tabId from aria-controls with hash prefix", () => {
    const mockDoc = new MockDocument();

    // Create tab with pane and button
    const tab1 = new MockElement("li");
    tab1.classList.add("tab");
    const anchor1 = new MockElement("a");
    anchor1.setAttribute("role", "tab");
    anchor1.setAttribute("aria-controls", "#done"); // With hash prefix
    tab1.appendChild(anchor1);
    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    mockDoc.setElement("done", donePane);
    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    mockDoc.addMockElement(tab1);
    mockDoc._activeElement = anchor1;

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const event = { key: "ArrowRight", preventDefault: () => {} };
      handleKeyDown(event);

      // Verify hash prefix was stripped and tab is active
      assert.strictEqual(
        donePane.style.display,
        "block",
        "Done pane should be visible",
      );
      assert.strictEqual(
        doneButton.classList.contains("is-active"),
        true,
        "Done button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });
});

describe("window load initialization", () => {
  it("should use data-default attribute value when present", () => {
    const mockDoc = new MockDocument();

    const tabsContainer = new MockElement("div");
    tabsContainer.classList.add("tabs");
    tabsContainer.dataset.default = "#processing";
    mockDoc.addMockElement(tabsContainer);

    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    mockDoc.setElement("done", donePane);
    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const processingPane = new MockElement("div");
    processingPane.classList.add("tab-pane");
    mockDoc.setElement("processing", processingPane);
    const processingButton = new MockElement("li");
    processingButton.classList.add("tab");
    mockDoc.setElement("processing-tab", processingButton);

    const doneTab = new MockElement("li");
    doneTab.classList.add("tab");
    const doneAnchor = new MockElement("a");
    doneAnchor.setAttribute("role", "tab");
    doneAnchor.setAttribute("aria-controls", "done");
    doneTab.appendChild(doneAnchor);

    const processingTab = new MockElement("li");
    processingTab.classList.add("tab");
    const processingAnchor = new MockElement("a");
    processingAnchor.setAttribute("role", "tab");
    processingAnchor.setAttribute("aria-controls", "processing");
    processingTab.appendChild(processingAnchor);

    mockDoc.addMockElement(doneTab);
    mockDoc.addMockElement(processingTab);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const defaultTab = initializeTabs(mockDoc);

      assert.strictEqual(
        defaultTab,
        "processing",
        "Should extract tab id from data-default without hash",
      );
      assert.strictEqual(
        processingPane.style.display,
        "block",
        "Processing pane should be visible",
      );
      assert.strictEqual(
        processingButton.classList.contains("is-active"),
        true,
        "Processing button should be active",
      );
      assert.strictEqual(
        doneAnchor.eventListeners.has("keydown"),
        true,
        "Keydown listener should be registered on tab anchors",
      );
      assert.strictEqual(
        processingAnchor.eventListeners.has("keydown"),
        true,
        "Keydown listener should be registered on all tab anchors",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should fallback to first tab aria-controls when data-default is missing", () => {
    const mockDoc = new MockDocument();

    const tabsContainer = new MockElement("div");
    tabsContainer.classList.add("tabs");
    mockDoc.addMockElement(tabsContainer);

    const errorPane = new MockElement("div");
    errorPane.classList.add("tab-pane");
    mockDoc.setElement("error", errorPane);
    const errorButton = new MockElement("li");
    errorButton.classList.add("tab");
    mockDoc.setElement("error-tab", errorButton);

    const firstTab = new MockElement("li");
    firstTab.classList.add("tab");
    firstTab.setAttribute("aria-controls", "#error");
    mockDoc.addMockElement(firstTab);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const defaultTab = initializeTabs(mockDoc);

      assert.strictEqual(
        defaultTab,
        "error",
        "Should fallback to first tab aria-controls",
      );
      assert.strictEqual(
        errorPane.style.display,
        "block",
        "Error pane should be visible",
      );
      assert.strictEqual(
        errorButton.classList.contains("is-active"),
        true,
        "Error button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it('should fallback to id.replace("-tab", "") when no aria-controls', () => {
    const mockDoc = new MockDocument();

    const tabsContainer = new MockElement("div");
    tabsContainer.classList.add("tabs");
    mockDoc.addMockElement(tabsContainer);

    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    mockDoc.setElement("done", donePane);
    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const firstTab = new MockElement("li");
    firstTab.classList.add("tab");
    firstTab.id = "done-tab";
    mockDoc.addMockElement(firstTab);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const defaultTab = initializeTabs(mockDoc);

      assert.strictEqual(
        defaultTab,
        "done",
        "Should parse tab id from element id",
      );
      assert.strictEqual(
        donePane.style.display,
        "block",
        "Done pane should be visible",
      );
      assert.strictEqual(
        doneButton.classList.contains("is-active"),
        true,
        "Done button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it('should use "done" as final fallback when no tabs container exists', () => {
    const mockDoc = new MockDocument();

    const donePane = new MockElement("div");
    donePane.classList.add("tab-pane");
    mockDoc.setElement("done", donePane);
    const doneButton = new MockElement("li");
    doneButton.classList.add("tab");
    mockDoc.setElement("done-tab", doneButton);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const defaultTab = initializeTabs(mockDoc);

      assert.strictEqual(
        defaultTab,
        "done",
        'Should use "done" as final fallback',
      );
      assert.strictEqual(
        donePane.style.display,
        "block",
        "Done pane should be visible",
      );
      assert.strictEqual(
        doneButton.classList.contains("is-active"),
        true,
        "Done button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });

  it("should extract aria-controls from nested anchor when tab lacks it", () => {
    const mockDoc = new MockDocument();

    const tabsContainer = new MockElement("div");
    tabsContainer.classList.add("tabs");
    mockDoc.addMockElement(tabsContainer);

    const processingPane = new MockElement("div");
    processingPane.classList.add("tab-pane");
    mockDoc.setElement("processing", processingPane);
    const processingButton = new MockElement("li");
    processingButton.classList.add("tab");
    mockDoc.setElement("processing-tab", processingButton);

    const firstTab = new MockElement("li");
    firstTab.classList.add("tab");
    const anchor = new MockElement("a");
    anchor.setAttribute("aria-controls", "#processing");
    firstTab.appendChild(anchor);
    mockDoc.addMockElement(firstTab);

    const originalDoc = global.document;
    try {
      global.document = mockDoc;

      const defaultTab = initializeTabs(mockDoc);

      assert.strictEqual(
        defaultTab,
        "processing",
        "Should extract aria-controls from nested anchor",
      );
      assert.strictEqual(
        processingPane.style.display,
        "block",
        "Processing pane should be visible",
      );
      assert.strictEqual(
        processingButton.classList.contains("is-active"),
        true,
        "Processing button should be active",
      );
    } finally {
      global.document = originalDoc;
    }
  });
});
