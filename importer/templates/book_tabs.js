function openTab(tabId) {
    const tabLinks = document.querySelectorAll(".tab");
    tabLinks.forEach(tab => {
        tab.classList.remove("is-active");
    });

    const tabPanes = document.querySelectorAll(".tab-pane");
    tabPanes.forEach(pane => {
        pane.style.display = "none";
    });

    document.getElementById(tabId).style.display = "block";
    document.getElementById(`${tabId}-tab`).classList.add("is-active");

    const tabAnchors = document.querySelectorAll('.tab a[role="tab"]');
    tabAnchors.forEach(anchor => {
        if (anchor.getAttribute('aria-controls') === tabId) {
            anchor.setAttribute('aria-selected', 'true');
        } else {
            anchor.setAttribute('aria-selected', 'false');
        }
    });
}

window.addEventListener('load', function () {
    const defaultTab = document.querySelector(".tabs").dataset.default
    openTab(defaultTab);
});