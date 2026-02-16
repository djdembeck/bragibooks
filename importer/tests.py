from django.test import TestCase, SimpleTestCase, Client, override_settings
from unittest.mock import patch, MagicMock
import json

from importer.views import build_directory_tree


@override_settings(
    ALLOWED_HOSTS=["localhost", "127.0.0.1", "testserver"], APPEND_SLASH=False
)
class DirectoryApiTests(TestCase):
    """Tests for the /api/directories/ API endpoint."""

    def setUp(self):
        """Set up test client."""
        self.client = Client()

    @patch("importer.views.Path")
    @patch("importer.views.directory_contents")
    def test_api_returns_200_with_valid_json_structure(
        self, mock_directory_contents, mock_path_class
    ):
        """Test that API returns 200 status with valid JSON structure."""
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
        mock_path_class.home.return_value = "/home/testuser"

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
        mock_path_class.home.return_value = "/home/testuser"

        response = self.client.get("/api/directories/", follow=True)

        self.assertEqual(response.status_code, 200)
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
        mock_path_class.home.return_value = "/home/testuser"

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
    def test_error_field_is_null_on_success(self, mock_path_class):
        """Test that error field is null when directory exists."""
        mock_input_path = MagicMock()
        mock_input_path.is_dir.return_value = True
        mock_input_path.exists.return_value = True

        mock_file = MagicMock()
        mock_file.name = "test.txt"
        mock_file.is_dir.return_value = False
        mock_input_path.iterdir.return_value = [mock_file]

        mock_path_class.return_value = mock_input_path
        mock_path_class.home.return_value = "/home/testuser"

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
        mock_path_class.home.return_value = "/home/testuser"

        response = self.client.get("/api/directories/", follow=True)
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
        mock_file.resolve.return_value = MagicMock().__str__ = "/path/file.txt"

        mock_subdir = MagicMock()
        mock_subdir.name = "subdir"
        mock_subdir.is_dir.return_value = True
        mock_subdir.__str__.return_value = "/path/subdir"
        mock_subdir.resolve.return_value = MagicMock().__str__ = "/path/subdir"

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
        mock_file.resolve.return_value = MagicMock().__str__ = "/path/root_file.txt"

        mock_subdir = MagicMock()
        mock_subdir.name = "subdir"
        mock_subdir.is_dir.return_value = True
        mock_subdir.__str__.return_value = "/path/subdir"
        mock_subdir.resolve.return_value = MagicMock().__str__ = "/path/subdir"

        # Create mock items for second level
        mock_nested_file = MagicMock()
        mock_nested_file.name = "nested.txt"
        mock_nested_file.is_dir.return_value = False
        mock_nested_file.__str__.return_value = "/path/subdir/nested.txt"
        mock_nested_file.resolve.return_value = MagicMock().__str__ = (
            "/path/subdir/nested.txt"
        )

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

        When a symlink points to a directory already in the visited set,
        the function should return an empty children list to prevent infinite loops.
        """
        # Create a mock directory that will be revisited (simulating a symlink cycle)
        mock_dir = MagicMock()
        mock_dir.name = "cycle_dir"
        mock_dir.is_dir.return_value = True
        mock_dir.__str__.return_value = "/path/cycle_dir"
        resolved_path = "/path/cycle_dir"
        mock_dir.resolve.return_value = MagicMock().__str__ = resolved_path

        # Create a mock file in the directory
        mock_file = MagicMock()
        mock_file.name = "file.txt"
        mock_file.is_dir.return_value = False
        mock_file.__str__.return_value = "/path/cycle_dir/file.txt"
        mock_file.resolve.return_value = MagicMock().__str__ = (
            "/path/cycle_dir/file.txt"
        )

        # Return the same directory twice to simulate a cycle
        mock_directory_contents.side_effect = [
            [mock_dir],  # First visit to cycle_dir
            [mock_file],  # Contents of cycle_dir
            [mock_dir],  # Second visit (cycle detected here)
        ]

        result = build_directory_tree("/some/path", max_depth=50)

        # First visit should work normally
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["name"], "cycle_dir")
        # Children should be present from first visit
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
            mock_dir.resolve.return_value = MagicMock().__str__ = resolved
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
        mock_file.resolve.return_value = MagicMock().__str__ = (
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

        This test verifies that the same resolved path is tracked across
        recursive calls to prevent infinite loops.
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
        # Children contain one entry from first visit (second visit returns empty due to cycle)
        self.assertEqual(len(result[0]["children"]), 1)
        self.assertEqual(result[0]["children"][0]["name"], "same_dir")
        self.assertEqual(result[0]["children"][0]["children"], [])
        # Verify directory_contents was called twice (root + subdir), not infinitely
        self.assertEqual(mock_directory_contents.call_count, 2)
