function openSearchPanel(srcPath, select_id) {
    const modal = document.getElementById('custom-search-modal');
    const modalTitle = modal.querySelector('.modal-card-title');
    modalTitle.textContent = `Custom Search: ${srcPath}`;
    modal.classList.add('is-active');
    modal.dataset.value = select_id;
}

function closeSearchPanel() {
    const modal = document.getElementById('custom-search-modal');

    // Clear the input fields
    modal.querySelector('#title').value = '';
    modal.querySelector('#author').value = '';
    modal.querySelector('#keywords').value = '';

    // Clear the search notification
    document.getElementById('search-notification').style.display = "none";

    // Close the panel
    modal.classList.remove("is-active");
}

function openRemoveConfirmationModal(label, column_index) {
    const modalLabel = document.querySelector("#confirm-modal-title");
    modalLabel.textContent = `Remove ${label} from search`;

    const modalButton = document.querySelector("#remove-column-button");
    modalButton.onclick = () => removeColumn(column_index)

    // Get the modal element and set it to active
    const modal = document.getElementById('remove-confirmation-modal');
    modal.classList.add('is-active');
}

function closeRemoveConfirmationModal() {
    // Get the modal element and remove the active class
    const modal = document.getElementById('remove-confirmation-modal');
    modal.classList.remove('is-active');
}

function removeColumn(column_index) {
    const columnToRemove = document.querySelector(`#asin-search-${column_index}`);
    columnToRemove.remove();

    // Close the modal
    closeRemoveConfirmationModal();

    // Check all searches have values
    checkAllSelectsHaveValue();
}

function constructQueryParams(media_dir, title, author, keywords) {
    let params = [];
    if (media_dir) {
        params.push(`media_dir=${encodeURIComponent(media_dir)}`);
    }
    if (title) {
        params.push(`title=${encodeURIComponent(title)}`);
    }
    if (author) {
        params.push(`author=${encodeURIComponent(author)}`);
    }
    if (keywords) {
        params.push(`keywords=${encodeURIComponent(keywords)}`);
    }
    return `?${params.join('&')}`;
}

async function search(url) {
    try {
        const response = await fetch(url);
        const data = response.json();
        return data;

    } catch (error) {
        console.error(`Error fetching options for select with url ${url}: ${error}`);
    }
}

function createOption(value, text, image_link) {
    const opt = document.createElement("option");
    if (value) {
        opt.value = value;
    }

    opt.text = text;
    opt.setAttribute("data-image-link", image_link);
    return opt;
}

function noOptionsFound(select) {
    select.style.borderColor = "red";
    select.style.borderWidth = "2px";
    let opt = createOption("", "No Audiobook results found, try a custom search...", "");
    select.appendChild(opt);
}

function updateOptions(select, data) {
    select.innerHTML = "";

    if (!data.length) {
        noOptionsFound(select);
        select.parentElement.classList.remove("is-loading");
        return;
    }

    select.removeAttribute("style");

    data.forEach(option => {
        text = option.title + " by " + option.author + " - Narrator " + option.narrator + ": " + option.asin;
        let opt = createOption(option.asin, text, option.image_link);
        select.appendChild(opt);
    });

    select.parentElement.classList.remove("is-loading");
}

function updateImage(counter) {
    // Get the selected value from the select element
    const selectElement = document.getElementById(`asin-select-${counter}`);

    // Get the corresponding image element
    const imageElement = document.getElementById(`image-${counter}`);

    // Update the image source
    let image_link = selectElement.options[selectElement.selectedIndex].dataset.imageLink;

    if (image_link) {
        imageElement.src = image_link;
    }
}

function checkAllSelectsHaveValue() {
    var hasValues = true;

    document.querySelectorAll(".asin-select").forEach(select => {
        if (select.value.length != 10) {
            hasValues = false;
            return;
        }
    });

    if (!hasValues) {
        document.getElementById("match-form-submit").disabled = true;
    } else {
        document.getElementById("match-form-submit").disabled = false;
    }
}

async function searchAsin(title, author, keywords) {
    const modal = document.getElementById('custom-search-modal');
    const select = document.getElementById(modal.dataset.value);

    // Build the query params and url
    let queryParams = constructQueryParams("", title, author, keywords);
    console.debug(queryParams);
    url = "asin-search" + queryParams;

    // Call the URL and get response
    let data = await search(url);

    if (!data.length) {
        // display message in search panel and return, dont close the search panel
        document.getElementById('search-notification').style.display = "block";
        return;
    }

    // Update the select for the calling custom search
    updateOptions(select, data)

    // close the search panel
    closeSearchPanel();

    // check all selects have a value and update submit button
    checkAllSelectsHaveValue();
}

async function fetchOptions() {
    document.querySelectorAll(".asin-select").forEach(async select => {
        const url = "asin-search" + constructQueryParams(select.name.split('/').pop());
        let data = await search(url);

        updateOptions(select, data);

        const counter = select.id.split('-').pop();
        updateImage(counter);
    });

    checkAllSelectsHaveValue();
}

fetchOptions();
checkAllSelectsHaveValue();
