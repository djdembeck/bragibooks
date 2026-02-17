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
            const folderId = arrow.id.replace("_arrow", "");
            expandFolder(folderId);
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
    const preLoader = doc.getElementById('pre-loader');
    if (preLoader) {
        preLoader.classList.add('hidden');
        setTimeout(function() {
            if (preLoader && preLoader.parentNode) {
                preLoader.parentNode.removeChild(preLoader);
            }
        }, 300);
    }
}

function generateId() {
    return 'id_' + Math.random().toString(36).substring(2, 11);
}

function findParentId(childPath, depth, parentMap) {
    const normalizedPath = childPath.replace(/\\/g, '/');
    const pathParts = normalizedPath.split('/');
    if (pathParts.length <= 1) return '';

    pathParts.pop();
    const parentPath = pathParts.join('/');

    if (parentMap.has(parentPath)) {
        return parentMap.get(parentPath);
    }

    return depth === 0 ? '' : '';
}

function countDirectoryItems(items) {
    let count = 0;
    items.forEach(item => {
        count++;
        if (item.children && item.children.length > 0) {
            count += countDirectoryItems(item.children);
        }
    });
    return count;
}

function updateProgress(current, total, status, currentFile) {
    const progressContainer = document.getElementById('progress-container');
    const progressBar = document.getElementById('progress-bar');
    const progressFill = document.getElementById('progress-fill');
    const progressStatus = document.getElementById('progress-status');
    const progressCount = document.getElementById('progress-count');
    const currentFileContainer = document.getElementById('current-file-container');
    const currentFileName = document.getElementById('current-file-name');

    if (progressContainer) {
        progressContainer.style.display = 'block';
    }

    if (total > 0) {
        if (progressBar) {
            progressBar.classList.add('determinate');
        }
        if (progressFill) {
            const percentage = Math.min(100, Math.max(0, (current / total) * 100));
            progressFill.style.width = percentage + '%';
        }
        if (progressCount) {
            progressCount.textContent = `${current} / ${total} items`;
        }
    } else {
        if (progressBar) {
            progressBar.classList.remove('determinate');
        }
        if (progressFill) {
            progressFill.style.width = '0%';
        }
        if (progressCount) {
            progressCount.textContent = '';
        }
    }

    if (progressStatus && status) {
        progressStatus.textContent = status;
    }

    if (currentFileContainer && currentFileName) {
        if (currentFile) {
            currentFileContainer.style.display = 'block';
            currentFileName.textContent = currentFile;
        } else {
            currentFileContainer.style.display = 'none';
        }
    }
}

async function fetchAndRenderDirectories() {
    const loadingEl = document.getElementById('directory-loading');
    const errorEl = document.getElementById('directory-error');
    const treeContainer = document.getElementById('directory-tree');
    const errorMessageEl = document.getElementById('directory-error-message');

    updateProgress(0, 0, 'Scanning directories...');

    if (loadingEl) loadingEl.style.display = '';
    if (errorEl) errorEl.style.display = 'none';
    if (treeContainer) treeContainer.innerHTML = '';

    try {
        const response = await fetch('/api/directories/');
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }
        const data = await response.json();

        if (data.error) {
            throw new Error(data.error);
        }

        if (treeContainer && data.directories) {
            const totalItems = countDirectoryItems(data.directories);
            let loadedItems = 0;
            let currentItemName = '';
            const container = treeContainer;
            let lastProgressUpdate = 0;

            const processItems = async (items, depth, parentId) => {
                for (const item of items) {
                    currentItemName = item.name;
                    buildDirectoryTree([item], container, depth, parentId, () => {
                        loadedItems++;
                    });

                    // Yield control every 50 items to allow UI updates
                    if (loadedItems % 50 === 0) {
                        await new Promise(resolve => setTimeout(resolve, 0));
                    }

                    if (item.children && item.children.length > 0) {
                        await processItems(item.children, depth + 1, generateId());
                    }
                }
            };

            const updateProgressLoop = () => {
                if (loadedItems > lastProgressUpdate) {
                    const itemNameAtUpdate = currentItemName;
                    updateProgress(loadedItems, totalItems, 'Loading files and folders...', itemNameAtUpdate);
                    lastProgressUpdate = loadedItems;
                }

                if (loadedItems < totalItems) {
                    requestAnimationFrame(updateProgressLoop);
                }
            };

            requestAnimationFrame(updateProgressLoop);
            await processItems(data.directories, 0, '');
        }

        if (loadingEl) loadingEl.style.display = 'none';
        initArrowListeners();
        initSearch();
        hideLoadingOverlay();
    } catch (error) {
        console.error('Failed to fetch directories:', error);
        hideLoadingOverlay();
        if (loadingEl) loadingEl.style.display = 'none';
        if (errorEl) errorEl.style.display = '';
        if (errorMessageEl) errorMessageEl.textContent = 'Failed to load directories: ' + error.message;
    }
}

function buildDirectoryTree(items, container, depth, parentId, onProgress = null) {
    const indent = '\u00A0'.repeat(5).repeat(depth);

    items.forEach(item => {
        const id = generateId();
        const isDirectory = item.is_directory;
        const display = depth === 0 ? '' : 'none';

        const label = document.createElement('label');
        label.className = isDirectory ? 'panel-block folder' : 'panel-block file';
        label.id = id;
        label.setAttribute('folder-id', parentId);
        label.style.display = display;

        if (isDirectory) {
            const contentDiv = document.createElement('div');

            const checkbox = document.createElement('input');
            checkbox.type = 'checkbox';
            checkbox.name = 'input_dir';
            checkbox.value = item.path;
            contentDiv.appendChild(checkbox);

            if (indent) {
                contentDiv.appendChild(document.createTextNode(indent));
            }

            const iconSpan = document.createElement('span');
            iconSpan.className = 'panel-icon';
            const iconI = document.createElement('i');
            iconI.className = 'fas fa-folder';
            iconI.setAttribute('aria-hidden', 'true');
            iconSpan.appendChild(iconI);
            contentDiv.appendChild(iconSpan);

            contentDiv.appendChild(document.createTextNode(item.name));
            label.appendChild(contentDiv);

            const arrowSpan = document.createElement('span');
            arrowSpan.className = 'arrow mr-2 is-medium';
            const arrowI = document.createElement('i');
            arrowI.className = 'fas fa-lg fa-angle-right';
            arrowI.id = id + '_arrow';
            arrowSpan.appendChild(arrowI);
            label.appendChild(arrowSpan);
        } else {
            const checkbox = document.createElement('input');
            checkbox.type = 'checkbox';
            checkbox.name = 'input_dir';
            checkbox.value = item.path;
            label.appendChild(checkbox);

            if (indent) {
                label.appendChild(document.createTextNode(indent));
            }

            const iconSpan = document.createElement('span');
            iconSpan.className = 'panel-icon';
            const iconI = document.createElement('i');
            iconI.className = 'fas fa-file';
            iconI.setAttribute('aria-hidden', 'true');
            iconSpan.appendChild(iconI);
            label.appendChild(iconSpan);

            label.appendChild(document.createTextNode(item.name));
        }

        container.appendChild(label);

        if (onProgress) {
            onProgress(1);
        }

        if (item.children && item.children.length > 0) {
            buildDirectoryTree(item.children, container, depth + 1, id, onProgress);
        }
    });
}

if (typeof document !== 'undefined') {
    document.addEventListener('DOMContentLoaded', () => {
        fetchAndRenderDirectories();

        const retryBtn = document.getElementById('directory-retry-btn');
        if (retryBtn) {
            retryBtn.addEventListener('click', (event) => {
                event.preventDefault();
                fetchAndRenderDirectories();
            });
        }
    });
}

// Export for testing (works in Node.js module context)
if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
        hideLoadingOverlay,
        expandFolder,
        initArrowListeners,
        initSearch,
        fuzzyMatch,
        resetPanel,
        generateId,
        countDirectoryItems,
        updateProgress,
        fetchAndRenderDirectories,
        buildDirectoryTree,
        findParentId
    };
}
