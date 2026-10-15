import unittest
import sys
import subprocess
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import promote_latest


class PromoteLatestTest(unittest.TestCase):
    def test_higher_version_promotes_even_with_older_timestamp(self):
        self.assertTrue(
            promote_latest.should_promote(
                "0.2.0",
                "2026-01-01T00:00:00Z",
                {"org.opencontainers.image.version": "0.1.9", "org.opencontainers.image.created": "2026-12-01T00:00:00Z"},
            )
        )

    def test_same_version_promotes_only_newer_build_timestamp(self):
        labels = {
            "org.opencontainers.image.version": "0.1.0",
            "org.opencontainers.image.created": "2026-10-14T10:00:00Z",
        }
        self.assertTrue(promote_latest.should_promote("0.1.0", "2026-10-14T11:00:00Z", labels))
        self.assertFalse(promote_latest.should_promote("0.1.0", "2026-10-14T10:00:00Z", labels))
        self.assertFalse(promote_latest.should_promote("0.1.0", "2026-10-14T09:00:00Z", labels))

    def test_older_version_never_promotes(self):
        self.assertFalse(
            promote_latest.should_promote(
                "0.1.9",
                "2027-01-01T00:00:00Z",
                {"org.opencontainers.image.version": "0.2.0", "org.opencontainers.image.created": "2026-01-01T00:00:00Z"},
            )
        )

    def test_missing_manifest_is_only_inspection_failure_treated_as_first_release(self):
        self.assertTrue(promote_latest.is_missing_manifest("ERROR: manifest unknown"))
        self.assertFalse(promote_latest.is_missing_manifest("manifest unknown: unauthorized"))
        self.assertFalse(promote_latest.is_missing_manifest("unauthorized: authentication required"))
        self.assertFalse(promote_latest.is_missing_manifest("network timeout"))

    def test_first_release_creates_latest(self):
        self.assertTrue(
            promote_latest.should_promote("1.0.0", "2026-01-01T00:00:00Z", None)
        )

    def test_existing_older_build_is_not_promoted(self):
        labels = {
            promote_latest.VERSION_LABEL: "1.0.0",
            promote_latest.CREATED_LABEL: "2026-10-14T12:00:00Z",
        }
        with patch("promote_latest.inspect_latest", return_value=labels), patch("promote_latest.subprocess.run") as run:
            self.assertFalse(promote_latest.promote("ghcr.io/acme/app", "sha256:old", "1.0.0", "2026-10-14T11:00:00Z"))
        run.assert_not_called()

    def test_missing_required_labels_fail_closed(self):
        with self.assertRaises(ValueError):
            promote_latest.should_promote("0.2.0", "2026-01-01T00:00:00Z", {})

    def test_registry_auth_error_does_not_treat_latest_as_missing(self):
        with patch("promote_latest.subprocess.run", return_value=subprocess.CompletedProcess([], 1, "", "unauthorized")):
            with self.assertRaises(RuntimeError):
                promote_latest.inspect_latest("ghcr.io/acme/app:latest")

    def test_missing_manifest_allows_first_publish(self):
        completed = subprocess.CompletedProcess([], 1, "", "manifest unknown")
        with patch("promote_latest.subprocess.run", return_value=completed) as run:
            self.assertTrue(promote_latest.promote("ghcr.io/acme/app", "sha256:digest", "1.0.0", "2026-01-01T00:00:00Z"))
        self.assertEqual(run.call_count, 2)

    def test_existing_equal_or_newer_release_skips_manifest_write(self):
        labels = {
            promote_latest.VERSION_LABEL: "1.0.0",
            promote_latest.CREATED_LABEL: "2026-10-14T10:00:00Z",
        }
        with patch("promote_latest.inspect_latest", return_value=labels), patch("promote_latest.subprocess.run") as run:
            self.assertFalse(promote_latest.promote("ghcr.io/acme/app", "sha256:digest", "1.0.0", "2026-10-14T10:00:00Z"))
        run.assert_not_called()

    def test_semver_prerelease_ordering(self):
        self.assertTrue(promote_latest.compare_versions("1.0.0", "1.0.0-rc.1") > 0)
        self.assertTrue(promote_latest.compare_versions("1.0.0-rc.2", "1.0.0-rc.1") > 0)
        self.assertTrue(promote_latest.compare_versions("1.0.0-rc.1", "1.0.0") < 0)


if __name__ == "__main__":
    unittest.main()
