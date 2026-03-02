import subprocess
import tempfile
from datetime import datetime
from pathlib import Path
from unittest.mock import MagicMock, patch

from django.test import TestCase, override_settings

from importer.models import Book, Setting, Status, StatusChoices
from utils.merge import run_m4b_merge
from utils.search_tools import SearchTool


class TestSubprocessMerge(TestCase):
    """Integration tests for subprocess-based m4b-merge functionality."""

    def setUp(self):
        """Set up test fixtures: Setting, Status, and Book objects."""
        # Create a Setting object (required for run_m4b_merge)
        self.setting = Setting.objects.create(
            api_url="https://api.audnex.us",
            completed_directory="/done",
            input_directory="/input",
            num_cpus=4,
            output_directory="/output",
            output_scheme="{author}/{title}",
        )

        # Create a Status object for the book
        self.status = Status.objects.create(
            status=StatusChoices.PROCESSING,
            message="",
        )

        # Create a test Book with a valid source path
        # Using a unique temp directory for tests
        self.test_src_path = tempfile.mkdtemp()
        self.test_asin = "B012345678"

        self.book = Book.objects.create(
            title="Test Book Title",
            asin=self.test_asin,
            short_desc="A test book description",
            long_desc="A longer test book description",
            release_date=datetime.strptime("2024-01-01", "%Y-%m-%d"),
            publisher="Test Publisher",
            lang="en",
            runtime_length_minutes=360,
            format_type="audiobook",
            converted=True,
            status=self.status,
            cover_image_link="https://example.com/cover.jpg",
            src_path=self.test_src_path,
            dest_path="",
        )

    def tearDown(self):
        """Clean up test fixtures."""
        # Clean up test directory
        import shutil

        if Path(self.test_src_path).exists():
            shutil.rmtree(self.test_src_path, ignore_errors=True)

    @patch("utils.merge.subprocess.run")
    def test_successful_merge(self, mock_subprocess_run):
        """Test successful merge execution with mocked subprocess.

        Verifies:
        - subprocess.run is called with correct arguments
        - Book status is updated to DONE
        - Book dest_path is updated from subprocess output
        """
        # Setup mock subprocess return value (successful execution)
        mock_result = MagicMock()
        mock_result.returncode = 0
        mock_result.stdout = (
            "Audiobook ASIN: job begin: /output/author/title.m4b\n"
            "Processing input files...\n"
            "job end\n"
        )
        mock_result.stderr = ""
        mock_subprocess_run.return_value = mock_result

        # Call the function under test
        run_m4b_merge(self.test_asin)

        # Refresh book from database
        self.book.refresh_from_db()
        self.book.status.refresh_from_db()

        # Assert subprocess.run was called
        mock_subprocess_run.assert_called_once()

        # Verify subprocess was called with correct arguments
        call_args = mock_subprocess_run.call_args
        cmd_list = call_args[0][0]  # First positional argument (the command list)

        # Check command structure
        self.assertEqual(cmd_list[0], "m4b-merge")
        self.assertIn("--inputs", cmd_list)
        self.assertIn(self.test_src_path, cmd_list)
        self.assertIn("--asin", cmd_list)
        self.assertIn(self.test_asin, cmd_list)
        self.assertIn("--api-url", cmd_list)
        self.assertIn("--output", cmd_list)
        self.assertIn("--completed-directory", cmd_list)
        self.assertIn("--num-cpus", cmd_list)
        self.assertIn("--path-format", cmd_list)
        self.assertIn("--log-level", cmd_list)

        # Verify subprocess.run kwargs
        call_kwargs = call_args[1] if len(call_args) > 1 else {}
        self.assertEqual(call_kwargs.get("timeout"), 14400)
        self.assertTrue(call_kwargs.get("capture_output"))
        self.assertTrue(call_kwargs.get("text"))

        # Assert book status is DONE
        self.assertEqual(self.book.status.status, StatusChoices.DONE)
        self.assertEqual(self.book.status.message, "")

        # Assert dest_path was updated from subprocess output
        self.assertEqual(self.book.dest_path, "/output/author/title.m4b")

    @patch("utils.merge.subprocess.run")
    def test_merge_failure(self, mock_subprocess_run):
        """Test merge failure handling with non-zero exit code.

        Verifies:
        - Book status is updated to ERROR
        - Status.message contains error information
        """
        # Setup mock subprocess return value (failed execution)
        mock_result = MagicMock()
        mock_result.returncode = 1
        mock_result.stdout = ""
        mock_result.stderr = "Error: Failed to process audio files"
        mock_subprocess_run.return_value = mock_result

        # Call the function under test
        run_m4b_merge(self.test_asin)

        # Refresh book from database
        self.book.refresh_from_db()
        self.book.status.refresh_from_db()

        # Assert subprocess.run was called
        mock_subprocess_run.assert_called_once()

        # Assert book status is ERROR
        self.assertEqual(self.book.status.status, StatusChoices.ERROR)

        # Assert error message contains failure info
        self.assertIn("m4b-merge failed", self.book.status.message)
        self.assertIn("exit code 1", self.book.status.message)
        self.assertIn("Error: Failed to process audio files", self.book.status.message)

    @patch("utils.merge.subprocess.run")
    def test_merge_timeout(self, mock_subprocess_run):
        """Test timeout handling when subprocess exceeds time limit.

        Verifies:
        - subprocess.TimeoutExpired is handled correctly
        - Book status is updated to ERROR
        - Status.message contains timeout information
        """
        # Setup mock to raise TimeoutExpired
        mock_subprocess_run.side_effect = subprocess.TimeoutExpired(
            cmd=["m4b-merge"],
            timeout=14400,
        )

        # Call the function under test
        with self.assertRaises(subprocess.TimeoutExpired):
            run_m4b_merge(self.test_asin)

        # Refresh book from database
        self.book.refresh_from_db()
        self.book.status.refresh_from_db()

        # Assert subprocess.run was called
        mock_subprocess_run.assert_called_once()

        # Assert book status is ERROR
        self.assertEqual(self.book.status.status, StatusChoices.ERROR)

        # Assert error message contains timeout info
        self.assertIn("timed out", self.book.status.message.lower())

    @patch("utils.merge.subprocess.run")
    def test_missing_input_path(self, mock_subprocess_run):
        """Test early validation when input path does not exist.

        Verifies:
        - subprocess.run is NOT called (validation fails early)
        - Book status is updated to ERROR
        - Status.message contains error about missing path
        """
        # Set book src_path to a non-existent path
        non_existent_path = "/this/path/does/not/exist"
        self.book.src_path = non_existent_path
        self.book.save()

        # Call the function under test
        run_m4b_merge(self.test_asin)

        # Refresh book from database
        self.book.refresh_from_db()
        self.book.status.refresh_from_db()

        # Assert subprocess.run was NOT called (early validation)
        mock_subprocess_run.assert_not_called()

        # Assert book status is ERROR
        self.assertEqual(self.book.status.status, StatusChoices.ERROR)

        # Assert error message mentions missing path
        self.assertIn("does not exist", self.book.status.message.lower())
        self.assertIn(non_existent_path, self.book.status.message)

    @patch("utils.merge.subprocess.run")
    def test_missing_settings(self, mock_subprocess_run):
        """Test handling when no settings exist in database.

        Verifies:
        - subprocess.run is NOT called (settings check fails early)
        - Book status is updated to ERROR
        - Status.message indicates no settings found
        """
        # Delete the settings object
        Setting.objects.all().delete()

        # Call the function under test
        run_m4b_merge(self.test_asin)

        # Refresh book from database
        self.book.refresh_from_db()
        self.book.status.refresh_from_db()

        # Assert subprocess.run was NOT called
        mock_subprocess_run.assert_not_called()

        # Assert book status is ERROR
        self.assertEqual(self.book.status.status, StatusChoices.ERROR)

        # Assert error message indicates no settings
        self.assertIn("no settings found", self.book.status.message.lower())

    @patch("utils.merge.subprocess.run")
    def test_output_path_parsing_failure(self, mock_subprocess_run):
        """Test handling when output path cannot be parsed from stdout.

        Verifies:
        - When stdout doesn't match expected format, a ValueError is raised
        - Book status is set to ERROR with appropriate message
        """
        # Setup mock subprocess return value with unparseable output
        mock_result = MagicMock()
        mock_result.returncode = 0
        mock_result.stdout = "Processing complete\nSome other output without path\n"
        mock_result.stderr = ""
        mock_subprocess_run.return_value = mock_result

        # Call the function under test - should raise ValueError
        with self.assertRaises(ValueError) as context:
            run_m4b_merge(self.test_asin)

        # Refresh book from database
        self.book.refresh_from_db()
        self.book.status.refresh_from_db()

        # Assert book status is ERROR
        self.assertEqual(self.book.status.status, StatusChoices.ERROR)

        # Assert error message indicates parsing failure
        self.assertIn("Could not parse output path", self.book.status.message)

        # Verify ValueError was raised with correct message
        self.assertIn("Could not parse output path", str(context.exception))


class TestSearchToolNormalizeName(TestCase):
    """Unit tests for SearchTool.normalize_name method."""

    def setUp(self):
        """Set up test fixtures."""
        self.tool = SearchTool(filename="test")

    def test_normalize_name_removes_read_by(self):
        """Verify 'read by' and narrator names are removed."""
        result = self.tool.normalize_name("Great Book read by John Smith")
        self.assertNotIn("read by", result.lower())
        self.assertNotIn("john smith", result.lower())
        self.assertIn("great", result.lower())
        self.assertIn("book", result.lower())

    def test_normalize_name_preserves_part_indicators(self):
        """Verify part/volume indicators are preserved for series matching."""
        result = self.tool.normalize_name("Series Book Part 1 of 3")
        self.assertIn("part 1", result.lower())
        self.assertIn("of 3", result.lower())
        self.assertIn("series", result.lower())
        self.assertIn("book", result.lower())

    def test_normalize_name_removes_years(self):
        """Verify years in parentheses are removed."""
        result = self.tool.normalize_name("Book Title (2020)")
        self.assertNotIn("2020", result)
        self.assertIn("book", result.lower())
        self.assertIn("title", result.lower())

    def test_normalize_name_keeps_title_words(self):
        """Verify actual title words are preserved."""
        result = self.tool.normalize_name("The Great Gatsby")
        self.assertIn("great", result.lower())
        self.assertIn("gatsby", result.lower())

    def test_normalize_name_removes_narrated_by(self):
        """Verify 'narrated by' and narrator names are removed."""
        result = self.tool.normalize_name("Great Book narrated by Jane Doe")
        self.assertNotIn("narrated by", result.lower())
        self.assertNotIn("jane doe", result.lower())
        self.assertIn("great", result.lower())
        self.assertIn("book", result.lower())

    def test_normalize_name_removes_standalone_years(self):
        """Verify standalone years are removed but other text preserved."""
        result = self.tool.normalize_name("Book Title 2020 Edition")
        self.assertNotIn("2020", result)
        self.assertIn("book", result.lower())
        self.assertIn("title", result.lower())
        self.assertIn("edition", result.lower())

    def test_normalize_name_preserves_volume_indicators(self):
        """Verify volume/chapter indicators are preserved for series matching."""
        result = self.tool.normalize_name("Epic Saga Volume 2")
        self.assertIn("volume 2", result.lower())
        self.assertIn("epic", result.lower())
        self.assertIn("saga", result.lower())

    def test_normalize_name_preserves_trailing_text_after_narrator(self):
        """Verify trailing text after narrator is preserved."""
        result = self.tool.normalize_name("Great Book read by John Smith The Sequel")
        self.assertNotIn("read by", result.lower())
        self.assertNotIn("john smith", result.lower())
        self.assertIn("great", result.lower())
        self.assertIn("book", result.lower())
        self.assertIn("sequel", result.lower())

    def test_normalize_name_preserves_book_indicators(self):
        """Verify book number indicators are preserved for series matching."""
        result = self.tool.normalize_name("Harry Potter Book 1")
        self.assertIn("book 1", result.lower())
        self.assertIn("harry", result.lower())
        self.assertIn("potter", result.lower())

    def test_normalize_name_preserves_chapter_indicators(self):
        """Verify chapter number indicators are preserved."""
        result = self.tool.normalize_name("Epic Saga Chapter 12")
        self.assertIn("chapter 12", result.lower())
        self.assertIn("epic", result.lower())
        self.assertIn("saga", result.lower())

    def test_normalize_name_preserves_part_of_total(self):
        """Verify part X of Y patterns are preserved."""
        result = self.tool.normalize_name("Dune Part 1 of 3")
        self.assertIn("part 1", result.lower())
        self.assertIn("of 3", result.lower())
        self.assertIn("dune", result.lower())

    def test_normalize_name_preserves_vol_abbreviation(self):
        """Verify vol abbreviation is preserved."""
        result = self.tool.normalize_name("Series Vol 2")
        self.assertIn("vol 2", result.lower())
        self.assertIn("series", result.lower())

    def test_normalize_name_preserves_large_numbers(self):
        """Verify large book/part numbers are preserved."""
        result = self.tool.normalize_name("Wheel of Time Book 14")
        self.assertIn("book 14", result.lower())
        self.assertIn("wheel", result.lower())
        self.assertIn("time", result.lower())

    def test_normalize_name_preserves_series_position_info(self):
        """Verify series position info helps distinguish between books."""
        result1 = self.tool.normalize_name("Dune Book 1")
        result2 = self.tool.normalize_name("Dune Book 2")
        self.assertIn("book 1", result1.lower())
        self.assertIn("book 2", result2.lower())
        self.assertNotEqual(result1, result2)
