#!/usr/bin/env python3

from __future__ import annotations

import argparse
import collections
import html
import re
import subprocess
import sys
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import unquote, urlsplit


DEFAULT_ROOT = Path(__file__).resolve().parent.parent
EXCLUDED_NAMES = {"AGENTS.md"}
EXCLUDED_PARTS = {".git", "dist"}
CHINESE_SUFFIXES = (".zh.md", ".zh-CN.md")
ROOT_PACKAGE_DOCS = (
    "README.md",
    "README.zh-CN.md",
    "NOTICE.md",
    "NOTICE.zh-CN.md",
    "SECURITY.md",
    "SECURITY.zh-CN.md",
    "CHANGELOG.md",
    "CHANGELOG.zh-CN.md",
)

FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")
HEADING_RE = re.compile(r"^ {0,3}(#{1,6})(?:[ \t]+|$)(.*?)\s*$")
SETEXT_HEADING_RE = re.compile(r"^ {0,3}(=+|-+)\s*$")
INLINE_CODE_RE = re.compile(r"(`+)(.+?)\1")
INLINE_LINK_START_RE = re.compile(r"!?\[([^\]]*)\]\(")
REFERENCE_LINK_RE = re.compile(
    r"^\s*\[([^\]]+)\]:\s*(<[^>]+>|\S+)", re.MULTILINE
)
HTML_LINK_RE = re.compile(r"\bhref=[\"']([^\"']+)[\"']", re.IGNORECASE)
AUTOLINK_RE = re.compile(r"<(https?://[^>\s]+)>")
HTML_ANCHOR_RE = re.compile(r"<(?:a|span)\b[^>]*(?:id|name)=[\"']([^\"']+)[\"']", re.IGNORECASE)
HAN_RE = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff]")
CHECKLIST_RE = re.compile(r"^\s*(?:[-*+]|\d+[.)])\s+\[([ xX])\]\s+")
TABLE_ROW_RE = re.compile(r"^\s*[^|]*\|.*$")
TABLE_SEPARATOR_CELL_RE = re.compile(r"^\s*:?-{3,}:?\s*$")
VERSION_RE = re.compile(
    r"(?<![A-Za-z0-9])v?\d+\.\d+(?:\.\d+)?(?:-[A-Za-z0-9][A-Za-z0-9.+-]*)?"
)
HASH_RE = re.compile(r"(?<![0-9A-Fa-f])[0-9A-Fa-f]{7,128}(?![0-9A-Fa-f])")
TIER_RE = re.compile(r"\b(?:Alpha|Beta|Preview)\b")
STATUS_RE = re.compile(r"\b(?:Supported|Unsupported)\b")
STATUS_CELL_RE = re.compile(r"\|\s*(Supported|Unsupported)\s*\|")
EXTERNAL_URL_RE = re.compile(r"https?://[^\s<>]+")
URL_TRAILING_PUNCTUATION = ".,;:!?，。；：！？"
ASCII_PROSE_RUN_RE = re.compile(r"[A-Za-z][A-Za-z'\",;:!?() \t\r\n-]{55,}")
ASCII_FUNCTION_WORDS = {
    "a",
    "an",
    "and",
    "are",
    "as",
    "be",
    "by",
    "for",
    "from",
    "in",
    "is",
    "its",
    "must",
    "not",
    "of",
    "on",
    "or",
    "that",
    "the",
    "this",
    "to",
    "when",
    "will",
    "with",
}


@dataclass(frozen=True)
class DocumentShape:
    headings: tuple[int, ...]
    fence_infos: tuple[str, ...]
    table_shapes: tuple[tuple[int, ...], ...]
    checklists: tuple[str, ...]


@dataclass(frozen=True)
class DocumentData:
    shape: DocumentShape
    fence_bodies: tuple[tuple[str, ...], ...]
    inline_code: collections.Counter[str]
    technical_literals: collections.Counter[str]
    prose: str
    prose_blocks: tuple[str, ...]
    technical_blocks: tuple[tuple[str, ...], ...]
    anchors: frozenset[str]
    heading_anchors: tuple[str, ...]
    table_technical: tuple[tuple[tuple[tuple[str, ...], ...], ...], ...]


@dataclass(frozen=True)
class MarkdownFenceData:
    headings: tuple[int, ...]
    table_shapes: tuple[tuple[int, ...], ...]
    checklists: tuple[str, ...]
    inline_code: collections.Counter[str]
    technical_literals: collections.Counter[str]
    prose: str
    prose_blocks: tuple[str, ...]
    technical_blocks: tuple[tuple[str, ...], ...]
    table_technical: tuple[tuple[tuple[tuple[str, ...], ...], ...], ...]


@dataclass(frozen=True)
class MarkdownLink:
    position: int
    end: int
    line: int
    kind: str
    label: str
    destination: str


def relative(root: Path, path: Path) -> str:
    return path.relative_to(root).as_posix()


def is_chinese(path: Path) -> bool:
    return path.name.endswith(CHINESE_SUFFIXES)


def counterpart_for(root: Path, english: Path) -> Path:
    suffix = ".zh-CN.md" if english.parent == root else ".zh.md"
    return english.with_name(english.name[:-3] + suffix)


def maintained_markdown(root: Path) -> list[Path]:
    paths: list[Path] = []
    for path in root.rglob("*.md"):
        rel = path.relative_to(root)
        if path.name in EXCLUDED_NAMES or any(part in EXCLUDED_PARTS for part in rel.parts):
            continue
        if path.is_file():
            paths.append(path)
    return sorted(paths)


def selector_markup(root: Path, path: Path, counterpart: Path) -> str:
    target = counterpart.name
    if is_chinese(path):
        return f'<p align="right"><a href="{target}">English</a> | <strong>简体中文</strong></p>'
    return f'<p align="right"><strong>English</strong> | <a href="{target}">简体中文</a></p>'


def selector_line(root: Path, path: Path, counterpart: Path) -> int | None:
    expected = selector_markup(root, path, counterpart)
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines()[:20], 1):
        if line.strip() == expected:
            return number
    return None


def inline_links(text: str) -> list[MarkdownLink]:
    links: list[MarkdownLink] = []
    for match in INLINE_LINK_START_RE.finditer(text):
        index = match.end()
        while index < len(text) and text[index] in " \t":
            index += 1
        destination_start = index
        destination_end = index
        closing = None

        if index < len(text) and text[index] == "<":
            destination_start = index + 1
            index += 1
            escaped = False
            while index < len(text):
                char = text[index]
                if escaped:
                    escaped = False
                elif char == "\\":
                    escaped = True
                elif char == ">":
                    destination_end = index
                    index += 1
                    break
                index += 1
            else:
                continue
        else:
            depth = 0
            escaped = False
            while index < len(text):
                char = text[index]
                if escaped:
                    escaped = False
                elif char == "\\":
                    escaped = True
                elif char == "(":
                    depth += 1
                elif char == ")":
                    if depth == 0:
                        destination_end = index
                        closing = index
                        break
                    depth -= 1
                elif char.isspace() and depth == 0:
                    destination_end = index
                    break
                index += 1
            if destination_end == destination_start:
                continue

        if closing is None:
            quote = ""
            escaped = False
            while index < len(text):
                char = text[index]
                if escaped:
                    escaped = False
                elif char == "\\":
                    escaped = True
                elif quote:
                    if char == quote:
                        quote = ""
                elif char in {'"', "'"}:
                    quote = char
                elif char == ")":
                    closing = index
                    break
                index += 1
        if closing is None:
            continue

        links.append(
            MarkdownLink(
                position=match.start(),
                end=closing + 1,
                line=text.count("\n", 0, match.start()) + 1,
                kind="inline",
                label=match.group(1),
                destination=text[destination_start:destination_end],
            )
        )
    return links


def replace_inline_links_with_labels(text: str) -> str:
    output = text
    for link in reversed(inline_links(text)):
        output = output[: link.position] + link.label + output[link.end :]
    return output


def heading_at(lines: list[str] | tuple[str, ...], index: int) -> tuple[int, str] | None:
    line = lines[index]
    match = HEADING_RE.match(line)
    if match:
        return len(match.group(1)), match.group(2).rstrip("#").rstrip()
    if not line.strip() or index + 1 >= len(lines):
        return None
    underline = SETEXT_HEADING_RE.match(lines[index + 1])
    if not underline:
        return None
    return (1 if underline.group(1).startswith("=") else 2), line.strip()


def split_blocks(text: str) -> tuple[str, ...]:
    return tuple(block.strip() for block in re.split(r"\n\s*\n", text) if block.strip())


def normalize_bare_url(value: str) -> str:
    value = value.rstrip(URL_TRAILING_PUNCTUATION)
    while value.endswith(")") and value.count("(") < value.count(")"):
        value = value[:-1]
    while value.endswith("]") and value.count("[") < value.count("]"):
        value = value[:-1]
    return value


def split_table_row(line: str) -> list[str]:
    stripped = line.strip()
    if stripped.startswith("|"):
        stripped = stripped[1:]
    if stripped.endswith("|"):
        stripped = stripped[:-1]

    cells: list[str] = []
    current: list[str] = []
    escaped = False
    code_ticks = 0
    index = 0
    while index < len(stripped):
        char = stripped[index]
        if escaped:
            current.append(char)
            escaped = False
            index += 1
            continue
        if char == "\\":
            current.append(char)
            escaped = True
            index += 1
            continue
        if char == "`":
            end = index
            while end < len(stripped) and stripped[end] == "`":
                end += 1
            run = end - index
            code_ticks = 0 if code_ticks == run else run
            current.extend(stripped[index:end])
            index = end
            continue
        if char == "|" and code_ticks == 0:
            cells.append("".join(current).strip())
            current = []
        else:
            current.append(char)
        index += 1
    cells.append("".join(current).strip())
    return cells


def github_slug(value: str) -> str:
    value = html.unescape(value)
    value = re.sub(r"!?\[([^\]]*)\]\([^)]*\)", r"\1", value)
    value = re.sub(r"<[^>]+>", "", value)
    value = INLINE_CODE_RE.sub(lambda match: match.group(2), value)
    value = value.strip().rstrip("#").strip().lower()
    output: list[str] = []
    for char in value:
        category = unicodedata.category(char)
        if char.isspace():
            output.append("-")
        elif char in "-_" or category[0] in {"L", "N"}:
            output.append(char)
    return re.sub(r"-+", "-", "".join(output)).strip("-")


def technical_literals(text: str) -> collections.Counter[str]:
    values: list[str] = []
    values.extend(VERSION_RE.findall(text))
    values.extend(HASH_RE.findall(text))
    values.extend(TIER_RE.findall(text))
    values.extend(STATUS_CELL_RE.findall(text))
    values.extend(normalize_bare_url(value) for value in EXTERNAL_URL_RE.findall(text))
    return collections.Counter(values)


def technical_identity(text: str, *, include_status: bool = False) -> tuple[str, ...]:
    visible = replace_inline_links_with_labels(text)
    values = [f"code:{match.group(2)}" for match in INLINE_CODE_RE.finditer(visible)]
    values.extend(
        f"literal:{literal}"
        for literal in sorted(
            technical_literals(INLINE_CODE_RE.sub("", visible)).elements()
        )
    )
    if include_status:
        values.extend(f"literal:{status}" for status in STATUS_RE.findall(visible))
    return tuple(sorted(values))


def table_technical_identity(
    rows: list[list[str]],
) -> tuple[tuple[tuple[str, ...], ...], ...]:
    return tuple(
        tuple(technical_identity(cell, include_status=True) for cell in row) for row in rows
    )


def markdown_fence_data(lines: tuple[str, ...]) -> MarkdownFenceData:
    headings: list[int] = []
    table_shapes: list[tuple[int, ...]] = []
    table_technical: list[tuple[tuple[tuple[str, ...], ...], ...]] = []
    table_rows: list[list[str]] = []
    checklists: list[str] = []
    inline_code: collections.Counter[str] = collections.Counter()
    prose_lines: list[str] = []
    visible_lines: list[str] = []
    external_links: list[str] = []

    def flush_table() -> None:
        nonlocal table_rows
        if len(table_rows) >= 2 and any(
            all(TABLE_SEPARATOR_CELL_RE.match(cell) for cell in row) for row in table_rows
        ):
            table_shapes.append(tuple(len(row) for row in table_rows))
            table_technical.append(table_technical_identity(table_rows))
        table_rows = []

    for index, line in enumerate(lines):
        if TABLE_ROW_RE.match(line):
            table_rows.append(split_table_row(line))
        else:
            flush_table()
        heading = heading_at(lines, index)
        if heading:
            headings.append(heading[0])
        checklist = CHECKLIST_RE.match(line)
        if checklist:
            checklists.append(checklist.group(1).lower())
        inline_code.update(match.group(2) for match in INLINE_CODE_RE.finditer(line))
        prose_line = INLINE_CODE_RE.sub("", line)
        prose_line = replace_inline_links_with_labels(prose_line)
        prose_line = re.sub(r"<[^>]+>", "", prose_line)
        reference = REFERENCE_LINK_RE.match(prose_line)
        if reference:
            prose_line = prose_line[: reference.start()]
        prose_lines.append(prose_line)
        visible_lines.append(line)
        raw_urls = [
            *(link.destination for link in inline_links(line)),
            *HTML_LINK_RE.findall(line),
            *AUTOLINK_RE.findall(line),
        ]
        reference = REFERENCE_LINK_RE.match(line)
        if reference:
            raw_urls.append(reference.group(2))
        for raw_url in raw_urls:
            normalized = html.unescape(raw_url.strip().strip("<>"))
            if urlsplit(normalized).scheme in {"http", "https"}:
                external_links.append(normalized)
    flush_table()
    prose = "\n".join(prose_lines)
    visible_blocks = split_blocks("\n".join(visible_lines))
    return MarkdownFenceData(
        headings=tuple(headings),
        table_shapes=tuple(table_shapes),
        checklists=tuple(checklists),
        inline_code=inline_code,
        technical_literals=technical_literals(prose) + collections.Counter(external_links),
        prose=prose,
        prose_blocks=split_blocks(prose),
        technical_blocks=tuple(technical_identity(block) for block in visible_blocks),
        table_technical=tuple(table_technical),
    )


def parse_document(root: Path, path: Path, errors: list[str]) -> DocumentData:
    headings: list[int] = []
    fence_infos: list[str] = []
    fence_bodies: list[tuple[str, ...]] = []
    current_fence_body: list[str] = []
    checklists: list[str] = []
    table_shapes: list[tuple[int, ...]] = []
    table_technical: list[tuple[tuple[tuple[str, ...], ...], ...]] = []
    table_rows: list[list[str]] = []
    inline_code: collections.Counter[str] = collections.Counter()
    prose_lines: list[str] = []
    visible_lines: list[str] = []
    anchors: set[str] = set()
    heading_anchors: list[str] = []
    slug_counts: collections.Counter[str] = collections.Counter()
    active_fence: tuple[str, int] | None = None
    technical_text: list[str] = []
    external_links: list[str] = []
    counterpart = None
    if is_chinese(path):
        base_name = path.name.removesuffix(".zh-CN.md").removesuffix(".zh.md") + ".md"
        counterpart = path.with_name(base_name)
    else:
        counterpart = counterpart_for(root, path)
    selector_number = selector_line(root, path, counterpart) if counterpart.is_file() else None

    def flush_table() -> None:
        nonlocal table_rows
        if len(table_rows) >= 2 and any(
            all(TABLE_SEPARATOR_CELL_RE.match(cell) for cell in row) for row in table_rows
        ):
            table_shapes.append(tuple(len(row) for row in table_rows))
            table_technical.append(table_technical_identity(table_rows))
        table_rows = []

    lines = path.read_text(encoding="utf-8").splitlines()
    for index, line in enumerate(lines):
        number = index + 1
        fence_match = FENCE_RE.match(line)
        if active_fence:
            if fence_match:
                marker = fence_match.group(1)
                if marker[0] == active_fence[0] and len(marker) >= active_fence[1]:
                    fence_bodies.append(tuple(current_fence_body))
                    current_fence_body = []
                    active_fence = None
                    visible_lines.append("")
                    continue
            current_fence_body.append(line)
            continue

        if fence_match:
            flush_table()
            marker = fence_match.group(1)
            active_fence = (marker[0], len(marker))
            fence_infos.append(fence_match.group(2).strip())
            visible_lines.append("")
            continue

        if TABLE_ROW_RE.match(line):
            table_rows.append(split_table_row(line))
        else:
            flush_table()

        heading = heading_at(lines, index)
        if heading:
            headings.append(heading[0])
            slug = github_slug(heading[1])
            if slug:
                duplicate = slug_counts[slug]
                anchor = slug if duplicate == 0 else f"{slug}-{duplicate}"
                anchors.add(anchor)
                heading_anchors.append(anchor)
                slug_counts[slug] += 1
        anchors.update(html.unescape(match) for match in HTML_ANCHOR_RE.findall(line))

        checklist_match = CHECKLIST_RE.match(line)
        if checklist_match:
            checklists.append(checklist_match.group(1).lower())

        if number == selector_number:
            visible_lines.append("")
            continue
        visible_lines.append(line)
        code_values = [match.group(2) for match in INLINE_CODE_RE.finditer(line)]
        inline_code.update(code_values)
        prose_line = INLINE_CODE_RE.sub("", line)
        prose_line = replace_inline_links_with_labels(prose_line)
        reference_url = REFERENCE_LINK_RE.match(line)
        reference = REFERENCE_LINK_RE.match(prose_line)
        if reference:
            prose_line = prose_line[: reference.start()]
        for raw_url in [
            *(link.destination for link in inline_links(line)),
            *HTML_LINK_RE.findall(line),
            *AUTOLINK_RE.findall(line),
            *([reference_url.group(2)] if reference_url else []),
        ]:
            parsed = urlsplit(html.unescape(raw_url.strip().strip("<>")))
            if parsed.scheme in {"http", "https"}:
                external_links.append(html.unescape(raw_url.strip().strip("<>")))
        prose_line = re.sub(r"<[^>]+>", "", prose_line)
        technical_text.append(prose_line)
        prose_lines.append(prose_line)

    flush_table()
    if active_fence:
        errors.append(f"{relative(root, path)}: unclosed fenced code block")

    prose = "\n".join(prose_lines)
    visible_blocks = split_blocks("\n".join(visible_lines))
    return DocumentData(
        shape=DocumentShape(
            tuple(headings), tuple(fence_infos), tuple(table_shapes), tuple(checklists)
        ),
        fence_bodies=tuple(fence_bodies),
        inline_code=inline_code,
        technical_literals=technical_literals("\n".join(technical_text))
        + collections.Counter(external_links),
        prose=prose,
        prose_blocks=split_blocks(prose),
        technical_blocks=tuple(technical_identity(block) for block in visible_blocks),
        anchors=frozenset(anchors),
        heading_anchors=tuple(heading_anchors),
        table_technical=tuple(table_technical),
    )


def document_links(path: Path) -> list[MarkdownLink]:
    visible_lines: list[str] = []
    active_fence: tuple[str, int] | None = None
    for line in path.read_text(encoding="utf-8").splitlines():
        fence_match = FENCE_RE.match(line)
        if active_fence:
            if fence_match:
                marker = fence_match.group(1)
                if marker[0] == active_fence[0] and len(marker) >= active_fence[1]:
                    active_fence = None
            visible_lines.append("")
            continue
        if fence_match:
            marker = fence_match.group(1)
            active_fence = (marker[0], len(marker))
            visible_lines.append("")
            continue
        visible_lines.append(line)

    visible = "\n".join(visible_lines)
    matches = inline_links(visible)
    for match in REFERENCE_LINK_RE.finditer(visible):
        matches.append(
            MarkdownLink(
                position=match.start(),
                end=match.end(),
                line=visible.count("\n", 0, match.start()) + 1,
                kind="reference",
                label=match.group(1).strip().casefold(),
                destination=match.group(2),
            )
        )
    for match in HTML_LINK_RE.finditer(visible):
        matches.append(
            MarkdownLink(
                position=match.start(),
                end=match.end(),
                line=visible.count("\n", 0, match.start()) + 1,
                kind="html",
                label="",
                destination=match.group(1),
            )
        )
    for match in AUTOLINK_RE.finditer(visible):
        matches.append(
            MarkdownLink(
                position=match.start(),
                end=match.end(),
                line=visible.count("\n", 0, match.start()) + 1,
                kind="autolink",
                label="",
                destination=match.group(1),
            )
        )
    return sorted(matches, key=lambda item: item.position)


def local_links(path: Path) -> list[tuple[int, str]]:
    return [(link.line, link.destination) for link in document_links(path)]


def external_link_identities(
    root: Path, path: Path, counterpart: Path
) -> tuple[tuple[str, str, str], ...]:
    selector_number = selector_line(root, path, counterpart)
    identities: list[tuple[str, str, str]] = []
    for link in document_links(path):
        if link.line == selector_number:
            continue
        destination = html.unescape(link.destination.strip().strip("<>"))
        if urlsplit(destination).scheme not in {"http", "https"}:
            continue
        label = link.label if link.kind == "reference" else ""
        identities.append((link.kind, label, destination))
    return tuple(identities)


def resolve_local(root: Path, source: Path, raw_url: str) -> tuple[Path, str] | None:
    url = html.unescape(raw_url.strip().strip("<>"))
    parsed = urlsplit(url)
    if parsed.scheme or parsed.netloc:
        return None
    decoded = unquote(parsed.path)
    if decoded.startswith("/"):
        target = root / decoded.lstrip("/")
    elif decoded:
        target = source.parent / decoded
    else:
        target = source
    return target.resolve(), unquote(parsed.fragment)


def counter_delta(
    left: collections.Counter[str], right: collections.Counter[str]
) -> tuple[list[str], list[str]]:
    missing = sorted((left - right).elements())
    added = sorted((right - left).elements())
    return missing, added


def validate_translation_coverage(
    label: str, english_prose: str, chinese_prose: str, errors: list[str]
) -> None:
    english_words = re.findall(r"[A-Za-z][A-Za-z0-9'-]*", english_prose)
    chinese_han = HAN_RE.findall(chinese_prose)
    english_size = len(re.sub(r"\s+", "", english_prose))
    chinese_size = len(re.sub(r"\s+", "", chinese_prose))
    if not chinese_han:
        errors.append(f"{label}: translated prose contains no Han text")
    if len(chinese_han) * 2 < len(english_words):
        errors.append(
            f"{label}: translated prose coverage is too small "
            f"({len(chinese_han)} Han characters for {len(english_words)} English words)"
        )
    if english_size and chinese_size * 4 < english_size:
        errors.append(f"{label}: translated prose is less than one quarter of the English source")


def contains_word_sequence(haystack: list[str], needle: list[str]) -> bool:
    if len(needle) > len(haystack):
        return False
    return any(
        haystack[index : index + len(needle)] == needle
        for index in range(len(haystack) - len(needle) + 1)
    )


def prose_words(text: str) -> list[str]:
    text = VERSION_RE.sub("", text)
    text = HASH_RE.sub("", text)
    text = EXTERNAL_URL_RE.sub("", text)
    return re.findall(r"[A-Za-z][A-Za-z'-]*", text)


def validate_prose_blocks(
    label: str,
    english_blocks: tuple[str, ...],
    chinese_blocks: tuple[str, ...],
    english_technical: tuple[tuple[str, ...], ...],
    chinese_technical: tuple[tuple[str, ...], ...],
    errors: list[str],
) -> None:
    if len(english_blocks) != len(chinese_blocks):
        errors.append(
            f"{label}: prose block count differs "
            f"({len(english_blocks)}/{len(chinese_blocks)})"
        )
        return
    if english_technical != chinese_technical:
        errors.append(f"{label}: technical literals differ between corresponding prose blocks")

    for index, (english_block, chinese_block) in enumerate(
        zip(english_blocks, chinese_blocks), 1
    ):
        english_words = prose_words(english_block)
        if len(english_words) >= 8 and not HAN_RE.search(chinese_block):
            errors.append(f"{label}: translated prose block {index} contains no Han text")

        english_sequence = [word.casefold() for word in english_words]
        for run in ASCII_PROSE_RUN_RE.findall(chinese_block):
            copied = [
                word.casefold() for word in re.findall(r"[A-Za-z][A-Za-z'-]*", run)
            ]
            if len(copied) < 10:
                continue
            if sum(word in ASCII_FUNCTION_WORDS for word in copied) < 3:
                continue
            if contains_word_sequence(english_sequence, copied):
                errors.append(
                    f"{label}: translated prose block {index} contains copied English prose"
                )
                break


def documentation_link_destinations(
    root: Path,
    path: Path,
    counterpart: Path,
    cache: dict[Path, DocumentData],
) -> list[tuple[Path, str]]:
    selector_number = selector_line(root, path, counterpart)
    targets: list[tuple[Path, str]] = []
    for line_number, raw_url in local_links(path):
        if line_number == selector_number:
            continue
        resolved = resolve_local(root, path, raw_url)
        if resolved is None:
            continue
        target, fragment = resolved
        targets.append((target.resolve(), fragment))
    return targets


def paired_destination(
    root: Path,
    target: Path,
    fragment: str,
    cache: dict[Path, DocumentData],
) -> tuple[Path, str]:
    target = target.resolve()
    expected_target = target
    if target in cache and not is_chinese(target):
        expected_target = counterpart_for(root, target).resolve()

    expected_fragment = fragment
    if fragment and target in cache and expected_target in cache:
        heading_anchors = cache[target].heading_anchors
        if fragment in heading_anchors:
            index = heading_anchors.index(fragment)
            paired_anchors = cache[expected_target].heading_anchors
            if index < len(paired_anchors):
                expected_fragment = paired_anchors[index]
    return expected_target, expected_fragment


def display_destination(root: Path, destination: tuple[Path, str]) -> str:
    target, fragment = destination
    try:
        value = relative(root, target)
    except ValueError:
        value = str(target)
    return f"{value}#{fragment}" if fragment else value


def validate_pair(
    root: Path,
    english: Path,
    chinese: Path,
    cache: dict[Path, DocumentData],
    errors: list[str],
) -> None:
    for path, counterpart in ((english, chinese), (chinese, english)):
        if selector_line(root, path, counterpart) is None:
            errors.append(
                f"{relative(root, path)}: expected reciprocal language selector within the first 20 lines"
            )

    english_data = cache[english.resolve()]
    chinese_data = cache[chinese.resolve()]
    english_targets = documentation_link_destinations(root, english, chinese, cache)
    chinese_targets = documentation_link_destinations(root, chinese, english, cache)
    expected_chinese_targets = [
        paired_destination(root, target, fragment, cache)
        for target, fragment in english_targets
    ]
    if expected_chinese_targets != chinese_targets:
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: local documentation "
            "destinations differ "
            f"(expected {[display_destination(root, item) for item in expected_chinese_targets]}, "
            f"found {[display_destination(root, item) for item in chinese_targets]})"
        )
    english_external = external_link_identities(root, english, chinese)
    chinese_external = external_link_identities(root, chinese, english)
    if english_external != chinese_external:
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: "
            "external link identities differ"
        )
    if english_data.shape != chinese_data.shape:
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: structural mismatch "
            f"(headings {english_data.shape.headings}/{chinese_data.shape.headings}, "
            f"fences {english_data.shape.fence_infos}/{chinese_data.shape.fence_infos}, "
            f"tables {english_data.shape.table_shapes}/{chinese_data.shape.table_shapes}, "
            f"checklists {english_data.shape.checklists}/{chinese_data.shape.checklists})"
        )
    if len(english_data.fence_bodies) != len(chinese_data.fence_bodies):
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: fenced code block count differs"
        )
    for index, (info, english_body, chinese_body) in enumerate(
        zip(
            english_data.shape.fence_infos,
            english_data.fence_bodies,
            chinese_data.fence_bodies,
        ),
        1,
    ):
        language = info.split(maxsplit=1)[0].lower() if info else ""
        if language not in {"markdown", "md"}:
            if english_body != chinese_body:
                errors.append(
                    f"{relative(root, english)} and {relative(root, chinese)}: "
                    f"fenced code body {index} differs"
                )
            continue

        english_markdown = markdown_fence_data(english_body)
        chinese_markdown = markdown_fence_data(chinese_body)
        english_markdown_shape = (
            english_markdown.headings,
            english_markdown.table_shapes,
            english_markdown.checklists,
        )
        chinese_markdown_shape = (
            chinese_markdown.headings,
            chinese_markdown.table_shapes,
            chinese_markdown.checklists,
        )
        if english_markdown_shape != chinese_markdown_shape:
            errors.append(
                f"{relative(root, english)} and {relative(root, chinese)}: "
                f"fenced Markdown structure {index} differs"
            )
        if english_markdown.table_technical != chinese_markdown.table_technical:
            errors.append(
                f"{relative(root, english)} and {relative(root, chinese)}: "
                f"fenced Markdown table technical identity {index} differs"
            )
        missing, added = counter_delta(
            english_markdown.inline_code, chinese_markdown.inline_code
        )
        if missing or added:
            errors.append(
                f"{relative(root, english)} and {relative(root, chinese)}: fenced Markdown "
                f"inline code {index} differs (missing {missing[:5]}, added {added[:5]})"
            )
        missing, added = counter_delta(
            english_markdown.technical_literals,
            chinese_markdown.technical_literals,
        )
        if missing or added:
            errors.append(
                f"{relative(root, english)} and {relative(root, chinese)}: fenced Markdown "
                f"technical literals {index} differ (missing {missing[:5]}, added {added[:5]})"
            )
        validate_translation_coverage(
            f"{relative(root, chinese)} fenced Markdown block {index}",
            english_markdown.prose,
            chinese_markdown.prose,
            errors,
        )
        validate_prose_blocks(
            f"{relative(root, chinese)} fenced Markdown block {index}",
            english_markdown.prose_blocks,
            chinese_markdown.prose_blocks,
            english_markdown.technical_blocks,
            chinese_markdown.technical_blocks,
            errors,
        )

    missing, added = counter_delta(english_data.inline_code, chinese_data.inline_code)
    if missing or added:
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: inline code differs "
            f"(missing {missing[:5]}, added {added[:5]})"
        )
    missing, added = counter_delta(
        english_data.technical_literals, chinese_data.technical_literals
    )
    if missing or added:
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: technical literals differ "
            f"(missing {missing[:5]}, added {added[:5]})"
        )
    if english_data.table_technical != chinese_data.table_technical:
        errors.append(
            f"{relative(root, english)} and {relative(root, chinese)}: "
            "table technical identity differs"
        )

    validate_translation_coverage(
        relative(root, chinese), english_data.prose, chinese_data.prose, errors
    )
    validate_prose_blocks(
        relative(root, chinese),
        english_data.prose_blocks,
        chinese_data.prose_blocks,
        english_data.technical_blocks,
        chinese_data.technical_blocks,
        errors,
    )


def package_files(root: Path, paths: list[Path]) -> list[str]:
    available = {relative(root, path) for path in paths}
    expected = [name for name in ROOT_PACKAGE_DOCS if name in available]
    expected.extend(
        sorted(
            path
            for path in available
            if "/" not in path and path not in set(ROOT_PACKAGE_DOCS)
        )
    )
    expected.extend(sorted(path for path in available if path.startswith("docs/")))
    return expected


def parse_make_variable(text: str, name: str) -> set[str]:
    lines = text.splitlines()
    values: list[str] = []
    collecting = False
    for line in lines:
        if not collecting:
            match = re.match(rf"^{re.escape(name)}\s*:?=\s*(.*)$", line)
            if not match:
                continue
            value = match.group(1)
            collecting = value.rstrip().endswith("\\")
        else:
            value = line.strip()
            collecting = value.rstrip().endswith("\\")
        values.extend(value.rstrip().removesuffix("\\").split())
        if values and not collecting:
            break
    return set(values)


def validate_distribution_layout(root: Path, paths: list[Path], errors: list[str]) -> None:
    expected = package_files(root, paths)
    root_documents = {path for path in expected if "/" not in path}
    manifest_path = root / "scripts/documentation-package-files.txt"
    if not manifest_path.is_file():
        errors.append("scripts/documentation-package-files.txt: missing package manifest")
    else:
        manifest = [
            line.strip()
            for line in manifest_path.read_text(encoding="utf-8").splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        ]
        if manifest != expected:
            missing = sorted(set(expected) - set(manifest))
            extra = sorted(set(manifest) - set(expected))
            errors.append(
                "scripts/documentation-package-files.txt: package manifest differs from maintained "
                f"documentation (missing {missing}, extra {extra}, or wrong order)"
            )

    goreleaser = root / ".goreleaser.yaml"
    if not goreleaser.is_file():
        errors.append(".goreleaser.yaml: missing release configuration")
    else:
        text = goreleaser.read_text(encoding="utf-8")
        for name in sorted(root_documents | {"LICENSE"}):
            if not re.search(rf"^\s+-\s+{re.escape(name)}\s*$", text, re.MULTILINE):
                errors.append(f".goreleaser.yaml: archive omits {name}")
        for entry in ("docs/**", "examples/**", "scripts/documentation-package-files.txt"):
            if not re.search(rf"^\s+-\s+{re.escape(entry)}\s*$", text, re.MULTILINE):
                errors.append(f".goreleaser.yaml: archive omits {entry}")

    makefile = root / "Makefile"
    if not makefile.is_file():
        errors.append("Makefile: missing build contract")
    else:
        text = makefile.read_text(encoding="utf-8")
        declared = parse_make_variable(text, "DOCUMENTATION_FILES")
        required = root_documents | {"LICENSE"}
        if not required.issubset(declared):
            errors.append(
                f"Makefile: DOCUMENTATION_FILES omits {sorted(required - declared)}"
            )
        for token in (
            "$(DOCUMENTATION_FILES)",
            "documentation-package-files.txt",
            "cp -R docs/.",
            "cp -R examples/.",
        ):
            if token not in text:
                errors.append(f"Makefile: install target omits {token}")

    installer = root / "scripts/install.sh"
    if not installer.is_file():
        errors.append("scripts/install.sh: missing curl installer")
    else:
        text = installer.read_text(encoding="utf-8")
        loop = re.search(r"for file in\s+(.+?); do", text, re.DOTALL)
        declared = set(loop.group(1).replace("\\\n", " ").split()) if loop else set()
        required = root_documents | {"LICENSE"}
        if not required.issubset(declared):
            errors.append(f"scripts/install.sh: root copy loop omits {sorted(required - declared)}")
        for token in (
            "documentation-package-files.txt",
            "validate_package_tree",
            "data_stage",
            'copy_tree "${extract_dir}/docs"',
            'copy_tree "${extract_dir}/examples"',
        ):
            if token not in text:
                errors.append(f"scripts/install.sh: package installation omits {token}")

    required_tokens = {
        ".github/workflows/release-dry-run.yml": ("*.md", "--list-package-files"),
        ".github/workflows/release.yml": ("check-documentation-package.sh tree",),
        "scripts/check-documentation-package.sh": (
            "documentation-package-files.txt",
            "examples/quickstart.apf.hcl",
        ),
        "scripts/test-install.sh": (
            "--list-package-files",
            "unreadable Chinese document",
        ),
        "scripts/validate-release.sh": ("check-documentation-package.sh",),
    }
    for rel, tokens in required_tokens.items():
        target = root / rel
        text = target.read_text(encoding="utf-8") if target.is_file() else ""
        for token in tokens:
            if token not in text:
                errors.append(f"{rel}: missing distribution layout assertion {token}")


def validate_gate_wiring(root: Path, errors: list[str]) -> None:
    makefile = root / "Makefile"
    if not makefile.is_file():
        errors.append("Makefile: missing build contract")
    else:
        text = makefile.read_text(encoding="utf-8")
        if not re.search(r"^docs-check:\s*$", text, re.MULTILINE):
            errors.append("Makefile: docs-check target is missing")
        if "scripts/check-docs.py $(DOCS_CHECK_ARGS)" not in text:
            errors.append("Makefile: docs-check target does not forward DOCS_CHECK_ARGS")
        check_match = re.search(r"^check:\s*(.*)$", text, re.MULTILINE)
        if not check_match or "docs-check" not in check_match.group(1).split():
            errors.append("Makefile: check target does not depend on docs-check")
        if not check_match or "test-docs" not in check_match.group(1).split():
            errors.append("Makefile: check target does not depend on test-docs")

    required_tokens = {
        ".github/workflows/ci.yml": (
            "fetch-depth: 0",
            "DOCS_CHANGED_FROM",
            "DOCS_CHECK_ARGS",
            "make docs-check",
            "make test-docs",
        ),
        "scripts/validate-release.sh": (
            "scripts/check-docs.py",
            "scripts/test-check-docs.py",
        ),
    }
    for rel, tokens in required_tokens.items():
        target = root / rel
        text = target.read_text(encoding="utf-8") if target.is_file() else ""
        for token in tokens:
            if token not in text:
                errors.append(f"{rel}: missing documentation gate assertion {token}")


def maintained_changed_path(path: Path) -> bool:
    return (
        path.suffix == ".md"
        and path.name not in EXCLUDED_NAMES
        and not any(part in EXCLUDED_PARTS for part in path.parts)
    )


def changed_counterpart(path: Path) -> Path:
    if path.name.endswith(".zh-CN.md"):
        return path.with_name(path.name.removesuffix(".zh-CN.md") + ".md")
    if path.name.endswith(".zh.md"):
        return path.with_name(path.name.removesuffix(".zh.md") + ".md")
    suffix = ".zh-CN.md" if len(path.parts) == 1 else ".zh.md"
    return path.with_name(path.name.removesuffix(".md") + suffix)


def validate_changed_pairs(root: Path, changed_from: str, errors: list[str]) -> None:
    command = [
        "git",
        "-C",
        str(root),
        "diff",
        "--name-only",
        "--diff-filter=ACDMRTUXB",
        "-z",
        f"{changed_from}...HEAD",
    ]
    result = subprocess.run(command, check=False, capture_output=True)
    if result.returncode != 0:
        detail = result.stderr.decode("utf-8", errors="replace").strip().splitlines()
        suffix = f": {detail[0]}" if detail else ""
        errors.append(f"cannot compare documentation changes from {changed_from}{suffix}")
        return

    changed = {
        Path(value.decode("utf-8", errors="surrogateescape"))
        for value in result.stdout.split(b"\0")
        if value
    }
    maintained = {path for path in changed if maintained_changed_path(path)}
    for path in sorted(maintained):
        counterpart = changed_counterpart(path)
        if counterpart not in maintained:
            errors.append(
                f"{path.as_posix()}: changed without counterpart "
                f"{counterpart.as_posix()} since {changed_from}"
            )


def validate(
    root: Path, check_layout: bool = True, changed_from: str | None = None
) -> tuple[list[str], int, int]:
    root = root.resolve()
    errors: list[str] = []
    paths = maintained_markdown(root)
    english = [path for path in paths if not is_chinese(path)]
    pairs: dict[Path, Path] = {}

    for path in english:
        counterpart = counterpart_for(root, path)
        pairs[path.resolve()] = counterpart.resolve()
        if not counterpart.is_file():
            errors.append(
                f"{relative(root, path)}: missing counterpart {relative(root, counterpart)}"
            )

    expected_chinese = set(pairs.values())
    for path in paths:
        if is_chinese(path) and path.resolve() not in expected_chinese:
            errors.append(f"{relative(root, path)}: Chinese document has no English counterpart")

    cache: dict[Path, DocumentData] = {}
    for path in paths:
        cache[path.resolve()] = parse_document(root, path, errors)

    for english_resolved, chinese_resolved in pairs.items():
        chinese = Path(chinese_resolved)
        if chinese.is_file():
            validate_pair(root, Path(english_resolved), chinese, cache, errors)

    for path in english:
        data = cache[path.resolve()]
        for line_number, line in enumerate(data.prose.splitlines(), 1):
            if HAN_RE.search(line):
                errors.append(
                    f"{relative(root, path)}:{line_number}: unexpected Han text in English prose"
                )

    language_by_path = {path.resolve(): is_chinese(path) for path in paths}
    reverse_pairs = {chinese: english_path for english_path, chinese in pairs.items()}
    for path in paths:
        path_resolved = path.resolve()
        counterpart = pairs.get(path_resolved) or reverse_pairs.get(path_resolved)
        allowed_selector_line = (
            selector_line(root, path, Path(counterpart)) if counterpart and Path(counterpart).is_file() else None
        )
        for line_number, raw_url in local_links(path):
            resolved = resolve_local(root, path, raw_url)
            if resolved is None:
                continue
            target, fragment = resolved
            try:
                target.relative_to(root)
            except ValueError:
                errors.append(
                    f"{relative(root, path)}:{line_number}: local link escapes repository: {raw_url}"
                )
                continue
            if not target.exists():
                errors.append(
                    f"{relative(root, path)}:{line_number}: local link target does not exist: {raw_url}"
                )
                continue
            if fragment and target.suffix == ".md" and target.resolve() in cache:
                if fragment not in cache[target.resolve()].anchors:
                    errors.append(
                        f"{relative(root, path)}:{line_number}: local Markdown fragment does not exist: {raw_url}"
                    )
            if target.suffix != ".md" or target.resolve() not in language_by_path:
                continue
            if language_by_path[target.resolve()] == is_chinese(path):
                continue
            if counterpart and target.resolve() == counterpart and line_number == allowed_selector_line:
                continue
            errors.append(
                f"{relative(root, path)}:{line_number}: cross-language documentation link: {raw_url}"
            )

    if check_layout:
        validate_gate_wiring(root, errors)
        validate_distribution_layout(root, paths, errors)
    if changed_from:
        validate_changed_pairs(root, changed_from, errors)
    return errors, len(pairs), len(package_files(root, paths))


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Validate AlpineForm bilingual documentation")
    parser.add_argument("--root", type=Path, default=DEFAULT_ROOT, help=argparse.SUPPRESS)
    parser.add_argument(
        "--content-only",
        action="store_true",
        help="skip repository gate-wiring and release/install layout checks",
    )
    parser.add_argument(
        "--list-package-files",
        action="store_true",
        help="print the expected packaged Markdown files and exit",
    )
    parser.add_argument(
        "--changed-from",
        metavar="REF",
        help="require every maintained Markdown change since REF to include its counterpart",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = args.root.resolve()
    paths = maintained_markdown(root)
    if args.list_package_files:
        print("\n".join(package_files(root, paths)))
        return 0

    errors, pair_count, package_count = validate(
        root,
        check_layout=not args.content_only,
        changed_from=args.changed_from,
    )
    if errors:
        print("Documentation validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print(
        f"Documentation validation passed: {pair_count} English/Chinese pairs and "
        f"{package_count} packaged Markdown files checked."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
