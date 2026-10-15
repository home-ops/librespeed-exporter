def common_notices():
    return {
        "LICENSE",
        "GPL-3.0.txt",
        "librespeed-cli-LGPL-3.0.txt",
        "go-module-licenses.tar.gz",
        "THIRD_PARTY.md",
    }


def required_assets(image_tag, release_tag, cli_version):
    image_assets = common_notices()
    release_assets = image_assets | {
        "sbom.spdx.json",
        "trivy-source.json",
        "trivy-image.json",
        f"librespeed-cli-{cli_version}-source.tar.gz",
        f"librespeed-cli-{cli_version}-source.tar.gz.sha256",
    }
    if image_tag == release_tag:
        return release_assets, release_assets
    return image_assets, release_assets


def validate_assets(image_tag, release_tag, image_names, release_names, cli_version):
    image_required, release_required = required_assets(image_tag, release_tag, cli_version)
    return image_required <= set(image_names) and release_required <= set(release_names)
