from django.test import TestCase, Client, override_settings
from unittest.mock import patch, MagicMock
import json
from pathlib import Path


@override_settings(
    ALLOWED_HOSTS=["localhost", "127.0.0.1", "testserver"], APPEND_SLASH=False
)
class DirectoryApiTests(TestCase):
    """Tests for the /api/directories/ API endpoint."""

    def setUp(self):
        """Set up test client."""
        self.client = Client()

    @patch("importer.views.Path")
    def test_api_returns_200_with_valid_json_structure(self, mock_path_class):
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
