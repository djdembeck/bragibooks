import json
from pathlib import Path
from unittest.mock import MagicMock, patch

from django.contrib.auth.models import User
from django.test import Client, SimpleTestCase, TestCase, override_settings

from importer.views import build_directory_tree


@override_settings(
    ALLOWED_HOSTS=["localhost", "127.0.0.1", "testserver"], APPEND_SLASH=False
)
class DirectoryApiTests(TestCase):
    """Tests for the /api/directories/ API endpoint."""

    def setUp(self):
        """Set up test client and user."""
        self.client = Client()
        self.user = User.objects.create_user(
            username="testuser", password="testpass123"
        )

    def test_unauthenticated_request_redirects_to_login(self):
        """Test that unauthenticated requests are redirected to login page."""
        response = self.client.get("/api/directories/")

        self.assertEqual(response.status_code, 302)
        self.assertIn("/login", response.url)

    @patch("importer.views.Path")
    @patch("importer.views.directory_contents")
    def test_api_returns_200_with_valid_json_structure(
        self, mock_directory_contents, mock_path_class
    ):
        """Test that API returns 200 status with valid JSON structure."""
        self.client.force_login(self.user)
        # Mock /input being a directory
        mock_input_path = MagicMock()
        mock_input_path.is_dir.return_value = True
        mock_input_path.exists.return_value = True

        # Mock directory contents
        mock_file = MagicMock()
        mock_file.name = "test_file.txt"
        mock_file.is_dir.return_value = False
        mock_input_path.iterdir.return_value = [mock_file]
        mock_directory_contents.return_value = [mock_file]

        mock_path_class.return_value = mock_input_path
        mock_path_class.home.return_value = mock_input_path

        response = self.client.get("/api/directories/", follow=True)

        self.assertEqual(response.status_code, 200)
        data = json.loads(response.content)

        # Verify JSON structure
        self.assertIn("directories", data)
        self.assertIn("error", data)
        self.assertIsInstance(data["directories"], list)
        self.assertIsNone(data["error"])

    @patch("importer.views.Path")
    def test_returns_empty_directories_when_root_not_found(self, mock_path_class):
        """Test that API returns empty directories when root directory doesn't exist."""
        # Mock /input not being a directory
        mock_input_path = MagicMock()
        mock_input_path.is_dir.return_value = False
        mock_input_path.exists.return_value = False

        mock_path_class.return_value = mock_input_path
        mock_path_class.home.return_value = mock_input_path

        response = self.client.get("/api/directories/", follow=True)

        self.assertEqual(response.status_code, 404)
        data = json.loads(response.content)

        # Verify empty directories and error message
        self.assertEqual(data["directories"], [])
        self.assertIn("error", data)
        self.assertIsNotNone(data["error"])
        self.assertIn("Directory not found", data["error"])

    @patch("importer.views.Path")
    @patch("importer.views.directory_contents")
    def test_returns_nested_structure_correctly(
        self, mock_directory_contents, mock_path_class
    ):
        """Test that API returns nested directory structure correctly."""
        self.client.force_login(self.user)
        # Mock /input being a directory
        mock_input_path = MagicMock()
        mock_input_path.is_dir.return_value = True
        mock_input_path.exists.return_value = True

        # Mock nested directory structure
        mock_subdir = MagicMock()
        mock_subdir.name = "subfolder"
        mock_subdir.is_dir.return_value = True
        mock_subdir.__str__.return_value = "/input/subfolder"

        mock_file = MagicMock()
        mock_file.name = "test_file.mp3"
        mock_file.is_dir.return_value = False
        mock_file.__str__.return_value = "/input/test_file.mp3"

        mock_input_path.iterdir.return_value = [mock_subdir, mock_file]
        mock_path_class.return_value = mock_input_path
        mock_path_class.home.return_value = mock_input_path

        # Mock subdirectory contents
        mock_nested_file = MagicMock()
        mock_nested_file.name = "nested.mp3"
        mock_nested_file.is_dir.return_value = False
        mock_nested_file.__str__.return_value = "/input/subfolder/nested.mp3"

        mock_directory_contents.side_effect = [
            [mock_subdir, mock_file],  # Root contents
            [mock_nested_file],  # Subfolder contents
        ]

        response = self.client.get("/api/directories/", follow=True)
        data = json.loads(response.content)

        # Verify nested structure
        self.assertEqual(len(data["directories"]), 2)

        # Check folder entry
        folder_entry = next(
            (item for item in data["directories"] if item["name"] == "subfolder"), None
        )
        self.assertIsNotNone(folder_entry)
        self.assertTrue(folder_entry["is_directory"])
        self.assertEqual(folder_entry["path"], "/input/subfolder")
        self.assertIsInstance(folder_entry["children"], list)
        self.assertEqual(len(folder_entry["children"]), 1)
        self.assertEqual(folder_entry["children"][0]["name"], "nested.mp3")

        # Check file entry
        file_entry = next(
            (item for item in data["directories"] if item["name"] == "test_file.mp3"),
            None,
        )
        self.assertIsNotNone(file_entry)
        self.assertFalse(file_entry["is_directory"])
        self.assertEqual(file_entry["children"], [])

    @patch("importer.views.Path")
    @patch("importer.views.directory_contents")
    def test_error_field_is_null_on_success(
        self, mock_directory_contents, mock_path_class
    ):
        """Test that error field is null when directory exists."""
        self.client.force_login(self.user)
        mock_input_path = MagicMock()
        mock_input_path.is_dir.return_value = True
        mock_input_path.exists.return_value = True

        mock_file = MagicMock()
        mock_file.name = "test.txt"
        mock_file.is_dir.return_value = False
        mock_input_path.iterdir.return_value = [mock_file]
        mock_directory_contents.return_value = [mock_file]

        mock_path_class.return_value = mock_input_path
        mock_path_class.home.return_value = mock_input_path

        response = self.client.get("/api/directories/", follow=True)
        data = json.loads(response.content)

        self.assertIsNone(data["error"])

    @patch("importer.views.Path")
    def test_error_field_has_message_on_failure(self, mock_path_class):
        """Test that error field has message when directory doesn't exist."""
        mock_input_path = MagicMock()
        mock_input_path.is_dir.return_value = False
        mock_input_path.exists.return_value = False

        mock_path_class.return_value = mock_input_path
        mock_path_class.home.return_value = mock_input_path

        response = self.client.get("/api/directories/", follow=True)

        self.assertEqual(response.status_code, 404)
        data = json.loads(response.content)

        self.assertIsNotNone(data["error"])
        self.assertIsInstance(data["error"], str)
        self.assertIn("Directory not found", data["error"])


class BuildDirectoryTreeTests(SimpleTestCase):
    """Tests for the build_directory_tree function safety features."""

    @patch("importer.views.directory_contents")
    def test_max_depth_zero_returns_empty_list(self, mock_directory_contents):
        """
        Test that max_depth=0 returns an empty list immediately.

        When max_depth is 0, the function should not recurse at all
        and should return an empty list.
        """
        result = build_directory_tree("/some/path", max_depth=0)
        self.assertEqual(result, [])
        mock_directory_contents.assert_not_called()

    @patch("importer.views.directory_contents")
    def test_max_depth_one_returns_root_level_only(self, mock_directory_contents):
        """
        Test that max_depth=1 returns only root level items without children.

        When max_depth is 1, the function should return items at the root
        level but their children should be empty lists (no recursion).
        """
        # Create mock items
        mock_file = MagicMock()
        mock_file.name = "file.txt"
        mock_file.is_dir.return_value = False
        mock_file.__str__.return_value = "/path/file.txt"
        mock_file.resolve.return_value = Path("/path/file.txt")

        mock_subdir = MagicMock()
        mock_subdir.name = "subdir"
        mock_subdir.is_dir.return_value = True
        mock_subdir.__str__.return_value = "/path/subdir"
        mock_subdir.resolve.return_value = Path("/path/subdir")

        mock_directory_contents.return_value = [mock_file, mock_subdir]

        result = build_directory_tree("/some/path", max_depth=1)

        # Should return both items with empty children
        self.assertEqual(len(result), 2)
        self.assertEqual(result[0]["name"], "file.txt")
        self.assertEqual(result[0]["children"], [])
        self.assertEqual(result[1]["name"], "subdir")
        self.assertEqual(result[1]["children"], [])

    @patch("importer.views.directory_contents")
    def test_max_depth_two_returns_two_levels(self, mock_directory_contents):
        """
        Test that max_depth=2 returns items two levels deep.

        When max_depth is 2, the function should return root level items
        and one level of children for directories.
        """
        # Create mock items for root level
        mock_file = MagicMock()
        mock_file.name = "root_file.txt"
        mock_file.is_dir.return_value = False
        mock_file.__str__.return_value = "/path/root_file.txt"
        mock_file.resolve.return_value = Path("/path/root_file.txt")

        mock_subdir = MagicMock()
        mock_subdir.name = "subdir"
        mock_subdir.is_dir.return_value = True
        mock_subdir.__str__.return_value = "/path/subdir"
        mock_subdir.resolve.return_value = Path("/path/subdir")

        # Create mock items for second level
        mock_nested_file = MagicMock()
        mock_nested_file.name = "nested.txt"
        mock_nested_file.is_dir.return_value = False
        mock_nested_file.__str__.return_value = "/path/subdir/nested.txt"
        mock_nested_file.resolve.return_value = Path("/path/subdir/nested.txt")

        # Configure side_effect to return different contents at each level
        mock_directory_contents.side_effect = [
            [mock_file, mock_subdir],  # Root level
            [mock_nested_file],  # Second level
        ]

        result = build_directory_tree("/some/path", max_depth=2)

        # Should return root items with subdir having one child
        self.assertEqual(len(result), 2)
        self.assertEqual(result[0]["name"], "root_file.txt")
        self.assertEqual(result[0]["children"], [])

        self.assertEqual(result[1]["name"], "subdir")
        self.assertEqual(len(result[1]["children"]), 1)
        self.assertEqual(result[1]["children"][0]["name"], "nested.txt")
        self.assertEqual(result[1]["children"][0]["children"], [])

    @patch("importer.views.directory_contents")
    def test_symlink_cycle_detection(self, mock_directory_contents):
        """
        Test that symlink cycles are detected using the visited set.

        When build_directory_tree processes a directory whose contents include
        a symlink back to a directory already in the visited set, the visited‑set
        logic skips reentering that directory so the final children list only
        contains the file.
        """
        # Create a mock directory that will be revisited (simulating a symlink cycle)
        mock_dir = MagicMock()
        mock_dir.name = "cycle_dir"
        mock_dir.is_dir.return_value = True
        mock_dir.__str__.return_value = "/path/cycle_dir"
        resolved_path = "/path/cycle_dir"
        mock_dir.resolve.return_value = Path(resolved_path)

        # Create a mock file in the directory
        mock_file = MagicMock()
        mock_file.name = "file.txt"
        mock_file.is_dir.return_value = False
        mock_file.__str__.return_value = "/path/cycle_dir/file.txt"
        mock_file.resolve.return_value = Path("/path/cycle_dir/file.txt")

        # Configure directory_contents to simulate a symlink cycle:
        # 1. Root path returns cycle_dir
        # 2. cycle_dir's contents include itself (self‑reference) + the file
        mock_directory_contents.side_effect = [
            [mock_dir],  # Root path ("cycle_dir")
            [mock_dir, mock_file],  # cycle_dir contents: self‑reference + file
        ]

        result = build_directory_tree("/some/path", max_depth=50)

        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["name"], "cycle_dir")
        # Children list contains only the file (directory skipped due to visited set)
        self.assertEqual(len(result[0]["children"]), 1)
        self.assertEqual(result[0]["children"][0]["name"], "file.txt")

    @patch("importer.views.directory_contents")
    def test_deeply_nested_directory_with_depth_limit(self, mock_directory_contents):
        """
        Test proper handling of deeply nested directories with depth limit.

        Verifies that the function correctly limits recursion depth
        even when there are many nested levels.
        """

        # Create a chain of nested directories
        def create_mock_dir(name, parent_path):
            mock_dir = MagicMock()
            mock_dir.name = name
            mock_dir.is_dir.return_value = True
            mock_dir.__str__.return_value = f"{parent_path}/{name}"
            resolved = f"{parent_path}/{name}"
            mock_dir.resolve.return_value = Path(resolved)
            return mock_dir

        # Create 5 levels of directories
        level0 = create_mock_dir("level0", "/path")
        level1 = create_mock_dir("level1", "/path/level0")
        level2 = create_mock_dir("level2", "/path/level0/level1")
        level3 = create_mock_dir("level3", "/path/level0/level1/level2")
        level4 = create_mock_dir("level4", "/path/level0/level1/level2/level3")

        # Mock file at deepest level
        mock_file = MagicMock()
        mock_file.name = "deep_file.txt"
        mock_file.is_dir.return_value = False
        mock_file.__str__.return_value = (
            "/path/level0/level1/level2/level3/level4/deep_file.txt"
        )
        mock_file.resolve.return_value = Path(
            "/path/level0/level1/level2/level3/level4/deep_file.txt"
        )

        # Configure directory contents for each level
        mock_directory_contents.side_effect = [
            [level0],  # Level 0
            [level1],  # Level 1
            [level2],  # Level 2
            [level3],  # Level 3
            [level4],  # Level 4
            [mock_file],  # Level 5 (should not be reached with max_depth=5)
        ]

        result = build_directory_tree("/path", max_depth=5)

        # With max_depth=5, we should get 5 levels of directories
        # Level 0 -> Level 1 -> Level 2 -> Level 3 -> Level 4 (no children)
        self.assertEqual(len(result), 1)  # Only level0 at root

        current = result[0]
        expected_depth = 5
        for i in range(expected_depth):
            self.assertEqual(current["name"], f"level{i}")
            if i < expected_depth - 1:
                self.assertEqual(len(current["children"]), 1)
                current = current["children"][0]
            else:
                # Last level should have no children (depth limit reached)
                self.assertEqual(current["children"], [])

    @patch("importer.views.directory_contents")
    def test_visited_set_prevents_infinite_recursion(self, mock_directory_contents):
        """
        Test that the visited set prevents infinite recursion on circular references.

        This test verifies that when directory_contents returns the same directory
        across multiple recursive calls, the self-reference detection prevents
        infinite recursion by skipping self-referential items.
        """
        from pathlib import Path

        # Create a mock directory that will be revisited
        mock_dir = MagicMock()
        mock_dir.name = "same_dir"
        mock_dir.is_dir.return_value = True
        mock_dir.__str__.return_value = "/path/same_dir"
        # Use a real Path object for resolve() so comparisons work correctly
        mock_dir.resolve.return_value = Path("/path/same_dir")

        # Return the same directory multiple times
        mock_directory_contents.return_value = [mock_dir]

        # Call with a very high max_depth - should not infinite loop
        result = build_directory_tree("/some/path", max_depth=100)

        # Should return one entry (directory is added on first visit)
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["name"], "same_dir")
        # The self-reference in the directory's contents is skipped (detected by
        # comparing resolved path with current directory's path), resulting in empty children
        self.assertEqual(len(result[0]["children"]), 0)
        # Verify directory_contents was called twice (root + one recursive call), not infinitely
        self.assertEqual(mock_directory_contents.call_count, 2)

    @patch("importer.views.directory_contents")
    def test_path_resolution_permission_error_returns_empty_list(
        self, mock_directory_contents
    ):
        """
        Test that path resolution raising PermissionError returns empty list.

        When Path.resolve() raises PermissionError, the function should return
        an empty list and log a debug message.
        """
        # Create a mock path that raises PermissionError on resolve()
        mock_path = MagicMock()
        mock_path.name = "unreadable_dir"
        mock_path.__str__.return_value = "/some/unreadable"

        # Make resolve() raise PermissionError
        mock_path.resolve.side_effect = PermissionError(
            "Permission denied: /some/unreadable"
        )

        # Configure directory_contents to return valid items
        # but they won't be reached because resolve() fails first
        mock_directory_contents.return_value = []

        result = build_directory_tree(mock_path)

        # Should return empty list
        self.assertEqual(result, [])
        # directory_contents should not be called because resolve failed
        mock_directory_contents.assert_not_called()

    @patch("importer.views.directory_contents")
    def test_path_resolution_os_error_returns_empty_list(self, mock_directory_contents):
        """
        Test that path resolution raising OSError returns empty list.

        When Path.resolve() raises OSError, the function should return
        an empty list and log a debug message.
        """
        # Create a mock path that raises OSError on resolve()
        mock_path = MagicMock()
        mock_path.name = "bad_link"
        mock_path.__str__.return_value = "/some/bad_link"

        # Make resolve() raise OSError
        mock_path.resolve.side_effect = OSError("Input/output error: /some/bad_link")

        # Configure directory_contents
        mock_directory_contents.return_value = []

        result = build_directory_tree(mock_path)

        # Should return empty list
        self.assertEqual(result, [])
        # directory_contents should not be called because resolve failed
        mock_directory_contents.assert_not_called()

    @patch("importer.views.directory_contents")
    def test_directory_access_permission_error_returns_empty_entries(
        self, mock_directory_contents
    ):
        """
        Test that directory access raising PermissionError returns empty entries list.

        When directory_contents() raises PermissionError, the function should return
        empty entries list and log a warning.
        """
        # Create a mock path
        mock_path = MagicMock()
        mock_path.name = "restricted_dir"
        mock_path.is_dir.return_value = True
        mock_path.__str__.return_value = "/restricted/dir"
        mock_path.resolve.return_value = Path("/restricted/dir")

        # Make directory_contents raise PermissionError
        mock_directory_contents.side_effect = PermissionError(
            "Permission denied: /restricted/dir"
        )

        result = build_directory_tree(mock_path)

        # Should return empty list (no entries added)
        self.assertEqual(result, [])
        # directory_contents should be called once
        mock_directory_contents.assert_called_once()

    @patch("importer.views.directory_contents")
    def test_directory_access_os_error_returns_empty_entries(
        self, mock_directory_contents
    ):
        """
        Test that directory access raising OSError returns empty entries list.

        When directory_contents() raises OSError, the function should return
        empty entries list and log a warning.
        """
        # Create a mock path
        mock_path = MagicMock()
        mock_path.name = "unreadable"
        mock_path.is_dir.return_value = True
        mock_path.__str__.return_value = "/unreadable"
        mock_path.resolve.return_value = Path("/unreadable")

        # Make directory_contents raise OSError
        mock_directory_contents.side_effect = OSError("I/O error: /unreadable")

        result = build_directory_tree(mock_path)

        # Should return empty list (no entries added)
        self.assertEqual(result, [])
        # directory_contents should be called once
        mock_directory_contents.assert_called_once()
