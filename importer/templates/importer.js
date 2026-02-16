function expandFolder(folderId) {
    const folderElement = document.querySelector(`.folder[id^='${folderId}']`);
    if (!folderElement) return;
    const arrow = folderElement.querySelector('.arrow i');
    if (!arrow) return;

    // Create and append loading spinner synchronously
    // This gives the browser a paint opportunity before expansion
    const loader = document.createElement('span');
    loader.className = 'folder-loading';
    folderElement.appendChild(loader);

    // Perform expansion synchronously (fast operation)
    const isExpanded = arrow.classList.contains('fa-rotate-90');
    const items = document.querySelectorAll(`.panel-block[folder-id^='${folderId}']`);

    arrow.classList.toggle('fa-rotate-90');

    items.forEach(item => {
        item.style.display = item.style.display === 'none' ? '' : 'none';
    });

    // Defer spinner removal to next frame so user sees it briefly
    requestAnimationFrame(() => {
        const spinner = folderElement.querySelector('.folder-loading');
        if (spinner) spinner.remove();
    });
}

function initArrowListeners() {
    const arrows = document.querySelectorAll(".arrow i");
    arrows.forEach(arrow => {
        arrow.addEventListener("click", (event) => {
            event.preventDefault();
            expandFolder(arrow.id);
        });
    });
}

function fuzzyMatch(needle, haystack) {
    let hlen = haystack.length;
    let nlen = needle.length;
    if (nlen > hlen) {
        return false;
    }
    if (nlen === hlen) {
        return needle === haystack;
    }
    outer: for (let i = 0, j = 0; i < nlen; i++) {
        const nch = needle.charCodeAt(i);
        while (j < hlen) {
            if (haystack.charCodeAt(j++) === nch) {
                continue outer;
            }
        }
        return false;
    }
    return true;
}

function resetPanel() {
    // reset the file explorer to its base config
    const panelBlock = document.querySelector('.panel-block-container');
    if (!panelBlock) return;

    const depth0 = panelBlock.querySelectorAll('label.panel-block[folder-id=""]');
    const everythingElse = panelBlock.querySelectorAll('label.panel-block:not([folder-id=""])');

    depth0.forEach(label => {
        label.style.display = '';
    });

    everythingElse.forEach(label => {
        label.style.display = 'none';
    });

    // Select the arrow element
    const arrows = document.querySelectorAll(`.arrow i`);

    // Toggle the rotation class on the arrow element
    arrows.forEach(arrow => {
        arrow.classList.remove('fa-rotate-90');
    });
}

function initSearch() {
    // Get the search input and panel elements
    const searchInput = document.getElementById('search-input');
    const panelBlock = document.querySelector('.panel-block-container');

    if (!searchInput || !panelBlock) return;

    // Add an input event listener to the search input
    searchInput.addEventListener('input', () => {
        // Get the search query
        let query = searchInput.value.toLowerCase();

        if (query) {
            // Loop through each label in the panel
            panelBlock.querySelectorAll('label').forEach(label => {
                // Get the label text
                const labelText = label.textContent.toLowerCase().trim();

                // Show or hide the label based on whether the query matches the label text
                if (fuzzyMatch(query, labelText)) {
                    label.style.display = '';
                } else {
                    label.style.display = 'none';
                }
            });
        } else {
            resetPanel();
        }
    });

    // add action to make the select all checkbox select/deselect all top level objects
    const selectAllCheckbox = document.getElementById("select-all-checkbox");
    const checkboxes = document.querySelectorAll('.panel-block-container label[folder-id=""] input[type="checkbox"]');
    if (selectAllCheckbox) {
        selectAllCheckbox.addEventListener("change", function () {
            checkboxes.forEach(function (checkbox) {
                checkbox.checked = selectAllCheckbox.checked;
            });
        });
    }

    const clearSearchButton = document.querySelector('.clear-search');
    if (clearSearchButton) {
        clearSearchButton.addEventListener('click', () => {
            searchInput.value = '';
            resetPanel();
        });
    }
}

/**
 * Hides the loading overlay when called.
 * Exported for testing purposes.
 * @param {Document} doc - The document object (defaults to global document)
 */
function hideLoadingOverlay(doc = document) {
    const loadingOverlay = doc.getElementById('loading-overlay');
    if (loadingOverlay && loadingOverlay.style) {
        loadingOverlay.style.display = 'none';
    }
}

 // Initialize on DOMContentLoaded for browser usage
 if (typeof document !== 'undefined') {
     document.addEventListener('DOMContentLoaded', () => {
         initArrowListeners();
         initSearch();
     });
 }

 // Export for testing (works in Node.js module context)
 if (typeof module !== 'undefined' && module.exports) {
     module.exports = { hideLoadingOverlay, expandFolder, initArrowListeners, initSearch, fuzzyMatch, resetPanel };
 }
