#!/usr/bin/env python3
import json
import os
import re
import subprocess
import sys
from datetime import datetime


VERSION_LABEL = "org.opencontainers.image.version"
CREATED_LABEL = "org.opencontainers.image.created"
SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$"
)


def parse_version(value):
    match = SEMVER.fullmatch(value)
    if not match:
        raise ValueError(f"invalid semantic version: {value}")
    major, minor, patch, prerelease = match.groups()
    identifiers = tuple(prerelease.split(".")) if prerelease else None
    if identifiers and any(
        identifier.isdigit() and len(identifier) > 1 and identifier.startswith("0")
        for identifier in identifiers
    ):
        raise ValueError(f"invalid semantic version: {value}")
    return int(major), int(minor), int(patch), identifiers


def compare_versions(left, right):
    left_core = parse_version(left)
    right_core = parse_version(right)
    if left_core[:3] != right_core[:3]:
        return (left_core[:3] > right_core[:3]) - (left_core[:3] < right_core[:3])
    left_pre, right_pre = left_core[3], right_core[3]
    if left_pre is None or right_pre is None:
        return (left_pre is None) - (right_pre is None)
    for left_id, right_id in zip(left_pre, right_pre):
        if left_id == right_id:
            continue
        left_numeric, right_numeric = left_id.isdigit(), right_id.isdigit()
        if left_numeric and right_numeric:
            return (int(left_id) > int(right_id)) - (int(left_id) < int(right_id))
        if left_numeric != right_numeric:
            return -1 if left_numeric else 1
        return (left_id > right_id) - (left_id < right_id)
    return (len(left_pre) > len(right_pre)) - (len(left_pre) < len(right_pre))


def parse_created(value):
    if not value:
        raise ValueError("image created label missing")
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def should_promote(candidate_version, candidate_created, current_labels):
    parse_version(candidate_version)
    parse_created(candidate_created)
    if current_labels is None:
        return True
    current_version = current_labels.get(VERSION_LABEL)
    current_created = current_labels.get(CREATED_LABEL)
    if not current_version or not current_created:
        raise ValueError("latest image lacks version or created label")
    version_order = compare_versions(candidate_version, current_version)
    if version_order != 0:
        return version_order > 0
    return parse_created(candidate_created) > parse_created(current_created)


def is_missing_manifest(stderr):
    text = stderr.lower()
    if any(marker in text for marker in ("unauthorized", "denied", "timeout", "connection refused", "server error")):
        return False
    return "manifest unknown" in text or "no such manifest" in text


def inspect_latest(image):
    result = subprocess.run(
        ["docker", "pull", image],
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode:
        error_text = result.stdout + result.stderr
        if is_missing_manifest(error_text):
            return None
        raise RuntimeError(f"cannot pull latest image: {error_text.strip()}")
    inspected = subprocess.run(
        ["docker", "image", "inspect", "--format", "{{json .Config.Labels}}", image],
        text=True,
        capture_output=True,
        check=False,
    )
    if inspected.returncode:
        raise RuntimeError(f"cannot inspect latest image labels: {inspected.stderr.strip()}")
    try:
        labels = json.loads(inspected.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("registry returned invalid image labels") from error
    if not isinstance(labels, dict):
        raise RuntimeError("registry returned no image label map")
    return labels


def promote(image, digest, version, created):
    current_labels = inspect_latest(f"{image}:latest")
    if not should_promote(version, created, current_labels):
        return False
    subprocess.run(
        ["docker", "buildx", "imagetools", "create", "--prefer-index=false", "--tag", f"{image}:latest", f"{image}@{digest}"],
        check=True,
    )
    return True


def main():
    if len(sys.argv) != 5:
        raise SystemExit("usage: promote_latest.py IMAGE DIGEST VERSION CREATED")
    image, digest, version, created = sys.argv[1:]
    if not promote(image, digest, version, created):
        print("latest promotion skipped: existing version is newer or not older build")
        promoted = "false"
    else:
        print("latest promoted")
        promoted = "true"
    if output := os.environ.get("GITHUB_OUTPUT"):
        with open(output, "a", encoding="utf-8") as stream:
            stream.write(f"promoted={promoted}\n")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
