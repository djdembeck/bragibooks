function openTab(event, tabId) {
    event.preventDefault();

    const tabLinks = document.querySelectorAll(".tab");
    tabLinks.forEach(tab => {
        tab.classList.remove("is-active");
    });

    const tabPanes = document.querySelectorAll(".tab-pane");
    tabPanes.forEach(pane => {
        pane.style.display = "none";
    });

    const tabElem = document.getElementById(tabId);
    const tabButton = document.getElementById(`${tabId}-tab`);
    if (tabElem) {
        tabElem.style.display = "block";
    } else {
        console.warn(`Tab element with id '${tabId}' not found`);
    }
    if (tabButton) {
        tabButton.classList.add("is-active");
    } else {
        console.warn(`Tab button with id '${tabId}-tab' not found`);
    }

    const tabAnchors = document.querySelectorAll('.tab a[role="tab"]');
    tabAnchors.forEach(anchor => {
        if (anchor.getAttribute('aria-controls') === tabId) {
            anchor.setAttribute('aria-selected', 'true');
            anchor.setAttribute('tabindex', '0');
            anchor.focus();
        } else {
            anchor.setAttribute('aria-selected', 'false');
            anchor.setAttribute('tabindex', '-1');
        }
    });
}

window.addEventListener('load', function () {
    const tabsContainer = document.querySelector(".tabs");
    let defaultTab;

    if (tabsContainer && tabsContainer.dataset.default) {
        defaultTab = tabsContainer.dataset.default;
    } else {
        if (!tabsContainer) {
            console.warn(".tabs container not found, using fallback tab");
        } else {
            console.warn("data-default attribute missing or empty, using fallback tab");
        }
        const firstTabButton = document.querySelector(".tab");
        if (firstTabButton) {
            defaultTab = firstTabButton.id.replace("-tab", "");
        } else {
            defaultTab = "done";
        }
    }
    openTab({ preventDefault: () => {} }, defaultTab);
});
