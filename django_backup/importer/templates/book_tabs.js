function openTab(event, tabId, userInitiated = false, doc = document) {
    if (event && typeof event.preventDefault === 'function') {
        event.preventDefault();
    }

    const normalizedId = tabId && tabId.toString().trim().replace(/^#/, '');

    if (!normalizedId) {
        console.warn(`Tab ID is blank or whitespace-only: '${tabId}'`);
        return;
    }

    const tabElem = doc.getElementById(normalizedId);
    const tabButton = doc.getElementById(`${normalizedId}-tab`);
    if (!tabElem) {
        console.warn(`Tab element with id '${normalizedId}' not found`);
        return;
    }
    if (!tabButton) {
        console.warn(`Tab button with id '${normalizedId}-tab' not found`);
        return;
    }

    const tabLinks = doc.querySelectorAll(".tab");
    tabLinks.forEach(tab => {
        tab.classList.remove("is-active");
    });

    const tabPanes = doc.querySelectorAll(".tab-pane");
    tabPanes.forEach(pane => {
        pane.style.display = "none";
    });

    tabElem.style.display = "block";
    tabButton.classList.add("is-active");

    const tabAnchors = doc.querySelectorAll('.tab a[role="tab"]');
    tabAnchors.forEach(anchor => {
        const anchorControls = anchor.getAttribute('aria-controls');
        const normalizedAnchorControls = anchorControls && anchorControls.trim().replace(/^#/, '');
        if (normalizedAnchorControls === normalizedId) {
            anchor.setAttribute('aria-selected', 'true');
            anchor.setAttribute('tabindex', '0');
            if (userInitiated) {
                anchor.focus();
            }
        } else {
            anchor.setAttribute('aria-selected', 'false');
            anchor.setAttribute('tabindex', '-1');
        }
    });
}

function handleKeyDown(event, doc = document) {
    const tabAnchors = Array.from(doc.querySelectorAll('.tab a[role="tab"]'));
    const currentIndex = tabAnchors.indexOf(doc.activeElement);

    if (currentIndex === -1 || tabAnchors.length === 0) return;

    let nextIndex = currentIndex;

    switch (event.key) {
        case 'ArrowLeft':
            nextIndex = currentIndex > 0 ? currentIndex - 1 : tabAnchors.length - 1;
            break;
        case 'ArrowRight':
            nextIndex = currentIndex < tabAnchors.length - 1 ? currentIndex + 1 : 0;
            break;
        case 'Home':
            nextIndex = 0;
            break;
        case 'End':
            nextIndex = tabAnchors.length - 1;
            break;
        default:
            return;
    }

    if (event && typeof event.preventDefault === 'function') {
        event.preventDefault();
    }
    const nextTab = tabAnchors[nextIndex];
    const tabIdRaw = nextTab.getAttribute('aria-controls');
    if (tabIdRaw) {
        const tabId = tabIdRaw.trim().replace(/^#/, "");
        openTab({ preventDefault: () => {} }, tabId, true, doc);
    } else {
        console.warn(`Tab anchor at index ${nextIndex} missing aria-controls attribute`);
    }
}

function resolveDefaultTab(tabsContainer, doc) {
    const firstTabButton = doc.querySelector(".tab");
    if (firstTabButton) {
        let ariaControls = firstTabButton.getAttribute("aria-controls");
        if (!ariaControls) {
            const anchorWithControls = firstTabButton.querySelector('[aria-controls]');
            if (anchorWithControls) {
                ariaControls = anchorWithControls.getAttribute("aria-controls");
            }
        }
        if (ariaControls) {
            const normalized = ariaControls.trim().replace(/^#/, "");
            if (normalized) {
                return normalized;
            }
        }
        const parsed = firstTabButton.id.replace("-tab", "");
        return parsed || "done";
    }
    return "done";
}

function initializeTabs(doc = document) {
    const tabsContainer = doc.querySelector(".tabs");
    let defaultTab;

    if (tabsContainer && tabsContainer.dataset.default) {
        defaultTab = tabsContainer.dataset.default.trim().replace(/^#/, "");
        // Guard against blank or whitespace-only values after normalization - treat same as missing
        if (!defaultTab) {
            console.warn("data-default normalized to empty, using fallback tab");
            defaultTab = resolveDefaultTab(tabsContainer, doc);
        }
    } else {
        if (!tabsContainer) {
            console.warn(".tabs container not found, using fallback tab");
        } else {
            console.warn("data-default attribute missing or empty, using fallback tab");
        }
        defaultTab = resolveDefaultTab(tabsContainer, doc);
    }

    // Validate that defaultTab corresponds to an actual tab element before activation
    const tabExists = doc.getElementById(defaultTab) && doc.getElementById(`${defaultTab}-tab`);
    if (!tabExists) {
        console.warn(`Tab '${defaultTab}' from data-default not found, using fallback tab`);
        defaultTab = resolveDefaultTab(tabsContainer, doc);
    }

    openTab({ preventDefault: () => {} }, defaultTab, false, doc);

    const tabAnchors = doc.querySelectorAll('.tab a[role="tab"]');
    tabAnchors.forEach(anchor => {
        if (!anchor.dataset.keydownBound) {
            anchor.addEventListener('keydown', (e) => handleKeyDown(e, doc));
            anchor.dataset.keydownBound = 'true';
        }
    });

    return defaultTab;
}

// Browser-only initialization (skipped during Node.js testing)
if (typeof window !== 'undefined') {
    window.addEventListener('load', function () {
        initializeTabs(document);
    });
}

// Export for testing
if (typeof module !== 'undefined' && module.exports) {
    module.exports = { openTab, handleKeyDown, initializeTabs };
}
