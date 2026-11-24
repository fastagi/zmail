#!/usr/bin/env python3

"""Enforce single-file line-count ceilings and mailbox.Service interface ratchets."""

from __future__ import annotations

import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[4]

MAX_LINES_NEW_FILE = 800
MAX_MAILBOX_SERVICE_DIRECT_METHODS = 0

# Legacy files temporarily above MAX_LINES_NEW_FILE with strict ratchet ceilings.
# Remove entries as files are decomposed; never raise an existing ceiling.
LEGACY_LARGE_FILE_CEILINGS: dict[str, int] = {
    "api/internal/capabilities/workflow/queue.go": 837,
    "api/internal/domain/mailbox.go": 1130,
    "api/internal/infra/config/config.go": 1256,
    "api/internal/infra/console/commands/make.go": 1372,
    "api/internal/modules/mailbox/dto.go": 1092,
    "api/internal/modules/mailbox/external_mail_imap.go": 1404,
    "api/internal/modules/mailbox/external_mail_push.go": 997,
    "api/internal/modules/mailbox/external_mail_tenant_service.go": 919,
    "api/internal/modules/mailbox/model.go": 874,
    "api/internal/modules/mailbox/repository.go": 1205,
    "api/internal/modules/mailbox/service.go": 1446,
    "api/internal/modules/webhook/repository.go": 1108,
    "web/src/features/mailbox/components/admin/roster-import-dialog.tsx": 910,
    "web/src/features/mailbox/components/compose-dialog.tsx": 901,
    "web/src/features/mailbox/components/mail-contacts.tsx": 1199,
    "web/src/features/mailbox/components/mail-domains.tsx": 1235,
    "web/src/features/mailbox/components/mail-templates.tsx": 1080,
    "web/src/features/mailbox/components/mailbox-access-panel.tsx": 859,
    "web/src/features/mailbox/components/message-list.tsx": 995,
    "web/src/features/mailbox/hooks/use-mailbox.ts": 818,
    "web/src/features/mailbox/schemas.ts": 1309,
    "web/src/features/mailbox/server/mock-mailbox-store.ts": 3088,
    "web/src/features/mailbox/services/mailbox-service.ts": 1570,
    "web/src/features/organization/server/mock-organization-store.ts": 910,
    "web/src/features/webhook/components/webhook-management.tsx": 984,
}

SCAN_ROOTS: tuple[tuple[str, set[str]], ...] = (
    ("api/internal", {".go"}),
    ("web/src", {".ts", ".tsx"}),
    ("admin/src", {".ts", ".tsx"}),
)

SKIPPED_MARKERS = (
    "/generated/",
    "/i18n/",
    "/test/",
    "/__tests__/",
    "_test.go",
    ".test.ts",
    ".test.tsx",
    "wire_gen.go",
    "routeTree.gen.ts",
)

SERVICE_INTERFACE_RE = re.compile(r"^type\s+Service\s+interface\s*\{(.*?)^\}", re.MULTILINE | re.DOTALL)
METHOD_LINE_RE = re.compile(r"^\s*[A-Z][A-Za-z0-9_]*\s*\(")


def check_file_sizes(failures: list[str]) -> None:
    seen_legacy: set[str] = set()
    for base, suffixes in SCAN_ROOTS:
        base_dir = ROOT / base
        if not base_dir.exists():
            continue
        for path in sorted(base_dir.rglob("*")):
            if not path.is_file() or path.suffix not in suffixes:
                continue
            rel = path.relative_to(ROOT).as_posix()
            if any(marker in rel for marker in SKIPPED_MARKERS):
                continue
            line_count = len(path.read_text(encoding="utf-8").splitlines())
            if rel in LEGACY_LARGE_FILE_CEILINGS:
                seen_legacy.add(rel)
                ceiling = LEGACY_LARGE_FILE_CEILINGS[rel]
                if line_count > ceiling:
                    failures.append(
                        f"{rel} has {line_count} lines, exceeding its ratchet ceiling of {ceiling}"
                    )
            elif line_count > MAX_LINES_NEW_FILE:
                failures.append(
                    f"{rel} has {line_count} lines, exceeding the {MAX_LINES_NEW_FILE}-line limit for new/refactored files"
                )


def check_mailbox_service_interface(failures: list[str]) -> None:
    service_path = ROOT / "api/internal/modules/mailbox/service.go"
    if not service_path.exists():
        return
    content = service_path.read_text(encoding="utf-8")
    match = SERVICE_INTERFACE_RE.search(content)
    if not match:
        failures.append("api/internal/modules/mailbox/service.go is missing type Service interface")
        return
    body = match.group(1)
    direct_methods = sum(1 for line in body.splitlines() if METHOD_LINE_RE.match(line))
    if direct_methods > MAX_MAILBOX_SERVICE_DIRECT_METHODS:
        failures.append(
            f"mailbox.Service declares {direct_methods} direct methods, exceeding ceiling {MAX_MAILBOX_SERVICE_DIRECT_METHODS}"
        )


def main() -> int:
    failures: list[str] = []
    check_file_sizes(failures)
    check_mailbox_service_interface(failures)
    if failures:
        print("Code size and interface guardrails failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1
    print("Code size and interface guardrails passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
