#!/usr/bin/env python3

from __future__ import annotations

import pathlib
import subprocess
import sys
import tempfile
import unittest
from urllib.parse import quote


CHECKER = pathlib.Path(__file__).with_name("check-docs.py")


class DocumentationGateTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)
        self.addCleanup(self.temporary.cleanup)
        self.build_fixture()

    def write(self, relative: str, text: str) -> None:
        target = self.root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text, encoding="utf-8")

    def read(self, relative: str) -> str:
        return (self.root / relative).read_text(encoding="utf-8")

    def replace(self, relative: str, old: str, new: str) -> None:
        text = self.read(relative)
        self.assertIn(old, text)
        self.write(relative, text.replace(old, new, 1))

    def build_fixture(self) -> None:
        self.write("AGENTS.md", "# Repository instructions\n")
        self.write("LICENSE", "fixture license\n")
        self.write(
            "README.md",
            """<p align="right"><strong>English</strong> | <a href="README.zh-CN.md">简体中文</a></p>

# Fixture Guide

Version `v3.24` remains Beta. Read the [details](docs/guide.md#details).

| Key | Value |
| --- | --- |
| branch | `v3.24` |

- [x] Verified

```bash
apf plan --offline
```
""",
        )
        self.write(
            "README.zh-CN.md",
            """<p align="right"><a href="README.md">English</a> | <strong>简体中文</strong></p>

# 测试指南

版本 `v3.24` 仍为 Beta。请阅读[详细说明](docs/guide.zh.md#详情)。

| 键 | 值 |
| --- | --- |
| 分支 | `v3.24` |

- [x] 已验证

```bash
apf plan --offline
```
""",
        )
        for stem in ("NOTICE", "SECURITY", "CHANGELOG"):
            self.write(
                f"{stem}.md",
                f'<p align="right"><strong>English</strong> | <a href="{stem}.zh-CN.md">简体中文</a></p>\n\n'
                f"# {stem.title()}\n\nMaintained English documentation.\n",
            )
            self.write(
                f"{stem}.zh-CN.md",
                f'<p align="right"><a href="{stem}.md">English</a> | <strong>简体中文</strong></p>\n\n'
                f"# {stem} 中文文档\n\n这是维护中的完整中文文档。\n",
            )
        self.write(
            "docs/guide.md",
            """<p align="right"><strong>English</strong> | <a href="guide.zh.md">简体中文</a></p>

# Details

This complete operational paragraph explains the maintained behavior, recovery procedure,
security boundary, and expected verification evidence. Return [home](../README.md).
""",
        )
        self.write(
            "docs/guide.zh.md",
            """<p align="right"><a href="guide.md">English</a> | <strong>简体中文</strong></p>

# 详情

这段完整的运维说明解释维护中的行为、恢复流程、安全边界以及预期的验证证据。
返回[首页](../README.zh-CN.md)。
""",
        )

        self.write(
            "Makefile",
            "DOCS_CHECK_ARGS ?=\n\n"
            "docs-check:\n\tpython3 scripts/check-docs.py $(DOCS_CHECK_ARGS)\n\n"
            "test-docs:\n\tpython3 scripts/test-check-docs.py\n\n"
            "check: docs-check test-docs\n",
        )
        self.write(
            "scripts/validate-release.sh",
            "python3 scripts/check-docs.py\npython3 scripts/test-check-docs.py\n",
        )
        self.write(
            ".github/workflows/ci.yml",
            "uses: actions/checkout\nwith:\n  fetch-depth: 0\n"
            "env:\n  DOCS_CHANGED_FROM: base\n"
            "run: make docs-check DOCS_CHECK_ARGS=--changed-from\n"
            "run: make test-docs\n",
        )

    def check(
        self, *, content_only: bool = True, changed_from: str | None = None
    ) -> subprocess.CompletedProcess[str]:
        command = [sys.executable, str(CHECKER), "--root", str(self.root)]
        if content_only:
            command.append("--content-only")
        if changed_from:
            command.extend(("--changed-from", changed_from))
        return subprocess.run(command, check=False, capture_output=True, text=True)

    def assert_fails(self, expected: str, *, content_only: bool = True) -> None:
        result = self.check(content_only=content_only)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(expected, result.stderr)

    def test_valid_content_and_gate_wiring_pass(self) -> None:
        content = self.check()
        self.assertEqual(content.returncode, 0, content.stderr)
        full = self.check(content_only=False)
        self.assertEqual(full.returncode, 0, full.stderr)

    def test_missing_and_misnamed_counterparts_fail(self) -> None:
        (self.root / "docs/guide.zh.md").unlink()
        self.assert_fails("missing counterpart docs/guide.zh.md")

    def test_non_root_zh_cn_name_is_rejected(self) -> None:
        (self.root / "docs/guide.zh.md").rename(self.root / "docs/guide.zh-CN.md")
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing counterpart docs/guide.zh.md", result.stderr)
        self.assertIn("Chinese document has no English counterpart", result.stderr)

    def test_selector_missing_late_or_reversed_fails(self) -> None:
        selector = self.read("README.zh-CN.md").splitlines()[0]
        self.replace("README.zh-CN.md", selector, "")
        self.assert_fails("expected reciprocal language selector")

        self.build_fixture()
        self.replace("README.zh-CN.md", selector, "\n" * 20 + selector)
        self.assert_fails("expected reciprocal language selector")

        self.build_fixture()
        self.replace(
            "README.zh-CN.md",
            selector,
            '<p align="right"><strong>English</strong> | <a href="README.md">简体中文</a></p>',
        )
        self.assert_fails("expected reciprocal language selector")

    def test_heading_fence_table_and_checklist_drift_fail(self) -> None:
        mutations = (
            ("# 测试指南", "## 测试指南", "structural mismatch"),
            ("```bash", "```sh", "structural mismatch"),
            ("apf plan --offline", "apf plan --refresh", "fenced code body"),
            ("| 分支 | `v3.24` |", "| 分支 | 值 | `v3.24` |", "structural mismatch"),
            ("- [x] 已验证", "- [ ] 已验证", "structural mismatch"),
        )
        for old, new, expected in mutations:
            with self.subTest(mutation=old):
                self.build_fixture()
                self.replace("README.zh-CN.md", old, new)
                self.assert_fails(expected)

    def test_setext_and_all_commonmark_checklist_markers_are_checked(self) -> None:
        for marker in ("+", "1."):
            with self.subTest(marker=marker):
                self.build_fixture()
                self.replace("README.md", "- [x] Verified", f"{marker} [x] Verified")
                self.replace(
                    "README.zh-CN.md", "- [x] 已验证", f"{marker} [ ] 已验证"
                )
                self.assert_fails("structural mismatch")

        self.build_fixture()
        self.write(
            "docs/setext.md",
            """<p align="right"><strong>English</strong> | <a href="setext.zh.md">简体中文</a></p>

Setext heading
==============

Complete translated heading fixture prose.
""",
        )
        self.write(
            "docs/setext.zh.md",
            """<p align="right"><a href="setext.md">English</a> | <strong>简体中文</strong></p>

Setext 标题
===========

完整的翻译标题 fixture 正文。
""",
        )
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.replace("docs/setext.zh.md", "===========", "-----------")
        self.assert_fails("structural mismatch")

    def test_unclosed_fence_fails(self) -> None:
        text = self.read("README.zh-CN.md")
        self.assertTrue(text.rstrip().endswith("```"))
        self.write("README.zh-CN.md", text.rstrip()[:-3] + "\n")
        self.assert_fails("unclosed fenced code block")

    def test_fenced_markdown_prose_must_be_translated(self) -> None:
        self.write(
            "docs/template.md",
            """<p align="right"><strong>English</strong> | <a href="template.zh.md">简体中文</a></p>

# Template

```markdown
## Summary

- Describe the complete user-visible purpose and operational boundary for `apf`.
```
""",
        )
        self.write(
            "docs/template.zh.md",
            """<p align="right"><a href="template.md">English</a> | <strong>简体中文</strong></p>

# 模板

```markdown
## 摘要

- 说明 `apf` 面向用户的完整用途和运维边界。
```
""",
        )
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)

        self.replace(
            "docs/template.zh.md",
            "## 摘要\n\n- 说明 `apf` 面向用户的完整用途和运维边界。",
            "## Summary\n\n- Describe the complete user-visible purpose and operational boundary for `apf`.",
        )
        self.assert_fails("fenced Markdown block 1: translated prose contains no Han text")

    def test_broken_escaping_cross_language_and_non_selector_links_fail(self) -> None:
        mutations = (
            ("docs/guide.zh.md#详情", "docs/missing.zh.md", "target does not exist"),
            ("docs/guide.zh.md#详情", "docs/guide.zh.md#不存在", "fragment does not exist"),
            ("../README.zh-CN.md", "../../../outside.md", "escapes repository"),
            ("../README.zh-CN.md", "../README.md", "cross-language documentation link"),
        )
        for old, new, expected in mutations:
            with self.subTest(mutation=new):
                self.build_fixture()
                target = "README.zh-CN.md" if old.startswith("docs/") else "docs/guide.zh.md"
                self.replace(target, old, new)
                self.assert_fails(expected)

        self.build_fixture()
        self.write(
            "docs/guide.zh.md",
            self.read("docs/guide.zh.md") + "\n另请参阅[英文原文](guide.md)。\n",
        )
        self.assert_fails("cross-language documentation link")

    def test_same_language_link_must_target_the_paired_destination(self) -> None:
        self.replace(
            "README.zh-CN.md",
            "docs/guide.zh.md#详情",
            "README.zh-CN.md#测试指南",
        )
        self.assert_fails("local documentation destinations differ")

    def test_fragment_must_match_the_corresponding_translated_heading(self) -> None:
        self.write(
            "docs/guide.md",
            self.read("docs/guide.md") + "\n## Recovery\n\nRecovery details.\n",
        )
        self.write(
            "docs/guide.zh.md",
            self.read("docs/guide.zh.md") + "\n## 恢复\n\n恢复详情。\n",
        )
        self.replace("README.zh-CN.md", "#详情", "#恢复")
        self.assert_fails("local documentation destinations differ")

    def test_non_markdown_local_destination_identity_is_checked(self) -> None:
        self.write("evidence/expected.go", "package evidence\n")
        self.write("evidence/wrong.go", "package evidence\n")
        self.write(
            "README.md",
            self.read("README.md") + "\nRead the [source](evidence/expected.go).\n",
        )
        self.write(
            "README.zh-CN.md",
            self.read("README.zh-CN.md") + "\n请阅读[源码](evidence/wrong.go)。\n",
        )
        self.assert_fails("local documentation destinations differ")

    def test_balanced_parenthesized_inline_url_is_checked(self) -> None:
        url = "https://example.invalid/a_(b)/tail"
        self.write("README.md", self.read("README.md") + f"\nRead [report]({url}).\n")
        self.write(
            "README.zh-CN.md", self.read("README.zh-CN.md") + f"\n请阅读[报告]({url})。\n"
        )
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.replace("README.zh-CN.md", "/tail", "/drift")
        self.assert_fails("external link identities differ")

    def test_percent_encoded_fragment_passes(self) -> None:
        encoded = quote("详情")
        self.replace("README.zh-CN.md", "#详情", f"#{encoded}")
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_han_in_english_prose_fails_but_code_is_allowed(self) -> None:
        self.write("docs/guide.md", self.read("docs/guide.md") + "\n未翻译正文\n")
        self.assert_fails("unexpected Han text in English prose")

        self.build_fixture()
        for relative in ("README.md", "README.zh-CN.md"):
            self.replace(relative, "apf plan --offline", "apf plan --offline\n# 中文代码")
            self.write(relative, self.read(relative) + "\nInline `中文代码` is protected.\n")
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_technical_literal_and_placeholder_drift_fail(self) -> None:
        self.replace("README.zh-CN.md", "`v3.24`", "`v3.23`")
        self.assert_fails("inline code differs")

        self.build_fixture()
        self.replace(
            "docs/guide.zh.md",
            "这段完整的运维说明解释维护中的行为、恢复流程、安全边界以及预期的验证证据。",
            "简述。",
        )
        self.replace("docs/guide.zh.md", "返回[首页]", "[首页]")
        self.assert_fails("translated prose coverage is too small")

    def test_long_copied_english_prose_in_chinese_fails(self) -> None:
        sentence = (
            "This newly documented recovery behavior changes the operator contract "
            "and must be translated."
        )
        self.write("docs/guide.md", self.read("docs/guide.md") + f"\n{sentence}\n")
        self.write("docs/guide.zh.md", self.read("docs/guide.zh.md") + f"\n{sentence}\n")
        self.assert_fails("contains copied English prose")

    def test_reference_definition_destinations_stay_with_their_labels(self) -> None:
        self.write(
            "README.md",
            self.read("README.md")
            + "\n[first]: https://example.invalid/first\n"
            + "[second]: https://example.invalid/second\n",
        )
        self.write(
            "README.zh-CN.md",
            self.read("README.zh-CN.md")
            + "\n[first]: https://example.invalid/second\n"
            + "[second]: https://example.invalid/first\n",
        )
        self.assert_fails("external link identities differ")

    def test_table_technical_literals_stay_with_their_rows(self) -> None:
        english = """

| Mode | Tier |
| --- | --- |
| guarded | Beta |
| narrow | Preview |
"""
        chinese = """

| 模式 | 层级 |
| --- | --- |
| guarded | Preview |
| narrow | Beta |
"""
        self.write("README.md", self.read("README.md") + english)
        self.write("README.zh-CN.md", self.read("README.zh-CN.md") + chinese)
        self.assert_fails("table technical identity differs")

    def test_external_links_hashes_versions_and_tiers_retain_identity(self) -> None:
        additions = {
            "README.md": """
Bare https://example.invalid/report. Autolink <https://example.invalid/private>.
[external-report]: https://example.invalid/reference
Release v9.8.7 with digest abcdef0123456789 remains Preview.
""",
            "README.zh-CN.md": """
直接链接 https://example.invalid/report。自动链接 <https://example.invalid/private>。
[external-report]: https://example.invalid/reference
带摘要 abcdef0123456789 的 release v9.8.7 仍为 Preview。
""",
        }
        mutations = (
            ("https://example.invalid/private", "https://evil.invalid/private"),
            ("https://example.invalid/reference", "https://evil.invalid/reference"),
            ("https://example.invalid/report", "https://evil.invalid/report"),
            ("abcdef0123456789", "fedcba9876543210"),
            ("v9.8.7", "v9.8.6"),
            ("Preview", "Beta"),
        )
        for old, new in mutations:
            with self.subTest(literal=old):
                self.build_fixture()
                for relative, addition in additions.items():
                    self.write(relative, self.read(relative) + addition)
                self.replace("README.zh-CN.md", old, new)
                self.assert_fails("differ")

    def test_changed_from_requires_both_sides_of_a_pair(self) -> None:
        subprocess.run(
            ["git", "init", "-q", str(self.root)],
            check=True,
            capture_output=True,
            text=True,
        )
        for key, value in (("user.name", "Docs Test"), ("user.email", "docs@example.invalid")):
            subprocess.run(
                ["git", "-C", str(self.root), "config", key, value],
                check=True,
                capture_output=True,
                text=True,
            )
        subprocess.run(
            ["git", "-C", str(self.root), "add", "."],
            check=True,
            capture_output=True,
            text=True,
        )
        subprocess.run(
            ["git", "-C", str(self.root), "commit", "-qm", "fixture"],
            check=True,
            capture_output=True,
            text=True,
        )
        base = subprocess.run(
            ["git", "-C", str(self.root), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()

        self.replace(
            "docs/guide.md",
            "maintained behavior",
            "newly documented maintained behavior",
        )
        subprocess.run(
            ["git", "-C", str(self.root), "add", "docs/guide.md"],
            check=True,
            capture_output=True,
            text=True,
        )
        subprocess.run(
            ["git", "-C", str(self.root), "commit", "-qm", "english only"],
            check=True,
            capture_output=True,
            text=True,
        )
        static = self.check()
        self.assertEqual(static.returncode, 0, static.stderr)
        changed = self.check(changed_from=base)
        self.assertNotEqual(changed.returncode, 0, changed.stdout)
        self.assertIn("changed without counterpart docs/guide.zh.md", changed.stderr)

        self.replace(
            "docs/guide.zh.md",
            "维护中的行为",
            "新记录的维护中行为",
        )
        subprocess.run(
            ["git", "-C", str(self.root), "add", "docs/guide.zh.md"],
            check=True,
            capture_output=True,
            text=True,
        )
        subprocess.run(
            ["git", "-C", str(self.root), "commit", "-qm", "paired change"],
            check=True,
            capture_output=True,
            text=True,
        )
        paired = self.check(changed_from=base)
        self.assertEqual(paired.returncode, 0, paired.stderr)

    def test_pipe_less_markdown_table_shape_is_checked(self) -> None:
        self.write(
            "docs/table.md",
            """<p align="right"><strong>English</strong> | <a href="table.zh.md">简体中文</a></p>

# Table

Key | Value
--- | ---
branch | current
""",
        )
        self.write(
            "docs/table.zh.md",
            """<p align="right"><a href="table.md">English</a> | <strong>简体中文</strong></p>

# 表格

键 | 值
--- | ---
分支 | 当前
""",
        )
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.replace("docs/table.zh.md", "分支 | 当前", "分支 | 当前 | 错误")
        self.assert_fails("structural mismatch")

    def test_local_and_hosted_gate_wiring_is_required(self) -> None:
        mutations = (
            (
                "Makefile",
                "scripts/check-docs.py $(DOCS_CHECK_ARGS)",
                "scripts/check-docs.py",
                "DOCS_CHECK_ARGS",
            ),
            (".github/workflows/ci.yml", "make docs-check", "make test", "make docs-check"),
            (
                ".github/workflows/ci.yml",
                "fetch-depth: 0",
                "fetch-depth: 1",
                "fetch-depth: 0",
            ),
            ("scripts/validate-release.sh", "check-docs.py", "other.py", "check-docs.py"),
        )
        for relative, old, new, expected in mutations:
            with self.subTest(relative=relative):
                self.build_fixture()
                self.replace(relative, old, new)
                self.assert_fails(expected, content_only=False)


if __name__ == "__main__":
    unittest.main()
