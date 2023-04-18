function openTab(tabId) {
    var tabPanes, tabLinks;
    
    tabLinks = document.querySelectorAll(".tab");
    tabLinks.forEach(tab => {
        tab.classList.remove("is-active");
    });

    tabPanes = document.querySelectorAll(".tab-pane");
    tabPanes.forEach(pane => {
        pane.style.display = "none";
    });

    document.getElementById(tabId).style.display = "block";
    document.getElementById(`${tabId}-tab`).classList.add("is-active");
}

window.addEventListener('load', function() {
    var defaultTab = document.querySelector(".tabs").dataset.default
    openTab(defaultTab);
});