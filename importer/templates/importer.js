function expandFolder(folderId) {
    // Select the arrow element
    const arrow = document.querySelector(`.folder[id^='${folderId}'] .arrow i`);

    // Toggle the rotation class on the arrow element
    arrow.classList.toggle('fa-rotate-90');

    // Select all items in the folder
    const items = document.querySelectorAll(`.panel-block[folder-id^='${folderId}']`);

    // Toggle the display style of each item
    items.forEach(item => {
        item.style.display = item.style.display === 'none' ? '' : 'none';
    });
}

const arrows = document.querySelectorAll(".arrow i");
arrows.forEach(arrow => {
    arrow.addEventListener("click", (event) => {
        event.preventDefault();
        expandFolder(arrow.id)
    });
});

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

// Get the search input and panel elements
const searchInput = document.getElementById('search-input');
const panelBlock = document.querySelector('.panel-block-container');

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
        resetPanel()
    }
});



// add action to make the select all checkbox select/deselect all top level objects
const selectAllCheckbox = document.getElementById("select-all-checkbox");
const checkboxes = document.querySelectorAll('.panel-block-container label[folder-id=""] input[type="checkbox"]');
selectAllCheckbox.addEventListener("change", function () {
    checkboxes.forEach(function (checkbox) {
        checkbox.checked = selectAllCheckbox.checked;
    });
});

const clearSearchButton = document.querySelector('.clear-search');
clearSearchButton.addEventListener('click', () => {
    searchInput.value = '';
    resetPanel()
});
