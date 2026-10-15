import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from release_assets import required_assets, validate_assets


class ReleaseAssetsTest(unittest.TestCase):
    def test_tag_release_and_build_release_have_different_asset_contracts(self):
        image, release = required_assets("v1.2.3", "1.2.3-20261014T120000Z-sha-run", "v1.0.14")
        self.assertEqual(
            image,
            {
                "LICENSE",
                "GPL-3.0.txt",
                "librespeed-cli-LGPL-3.0.txt",
                "go-module-licenses.tar.gz",
                "THIRD_PARTY.md",
            },
        )
        self.assertEqual(
            release,
            image
            | {
                "sbom.spdx.json",
                "trivy-source.json",
                "trivy-image.json",
                "librespeed-cli-v1.0.14-source.tar.gz",
                "librespeed-cli-v1.0.14-source.tar.gz.sha256",
            },
        )

    def test_scheduled_release_uses_one_full_asset_set(self):
        image, release = required_assets(
            "1.2.3-20261014T120000Z-sha-run",
            "1.2.3-20261014T120000Z-sha-run",
            "v1.0.14",
        )
        self.assertEqual(image, release)
        self.assertIn("sbom.spdx.json", image)
        self.assertIn("trivy-source.json", image)
        self.assertIn("trivy-image.json", image)

    def test_tag_release_validates_notice_only_build_and_full_version_release(self):
        image_required, release_required = required_assets(
            "v1.2.3", "1.2.3-20261014T120000Z-sha-run", "v1.0.14"
        )
        image_names = image_required
        release_names = release_required
        self.assertTrue(
            validate_assets(
                "v1.2.3",
                "1.2.3-20261014T120000Z-sha-run",
                image_names,
                release_names,
                "v1.0.14",
            )
        )
        self.assertFalse(
            validate_assets(
                "v1.2.3",
                "1.2.3-20261014T120000Z-sha-run",
                image_names - {"LICENSE"},
                release_names,
                "v1.0.14",
            )
        )
        self.assertFalse(
            validate_assets(
                "v1.2.3",
                "1.2.3-20261014T120000Z-sha-run",
                image_names,
                release_names - {"sbom.spdx.json"},
                "v1.0.14",
            )
        )

    def test_scheduled_release_validates_full_asset_set_on_same_tag(self):
        tag = "1.2.3-20261014T120000Z-sha-run"
        required = required_assets(tag, tag, "v1.0.14")[0]
        self.assertTrue(validate_assets(tag, tag, required, required, "v1.0.14"))
        self.assertFalse(validate_assets(tag, tag, required, required - {"sbom.spdx.json"}, "v1.0.14"))

    def test_release_asset_contract_requires_go_module_notices(self):
        tag = "1.2.3-20261014T120000Z-sha-run"
        required = required_assets(tag, tag, "v1.0.14")[0]
        self.assertIn("go-module-licenses.tar.gz", required)
        self.assertFalse(validate_assets(tag, tag, required - {"go-module-licenses.tar.gz"}, required, "v1.0.14"))


if __name__ == "__main__":
    unittest.main()
