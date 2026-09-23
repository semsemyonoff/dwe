#!/usr/bin/env python3
"""Compact, token-cheap text view over Tempo traces — for an AI coding agent
or a human in a terminal to look up what a request did (SQL, slow spans,
N+1 patterns, exceptions) without opening Grafana.

Stdlib only (must run on the app container's system python3, 3.9+ compatible).
Talks to Tempo's HTTP API (search + trace-by-id) and to the OTel collector's
OTLP/HTTP ingest endpoint (`selftest` only).

Subcommands: summary | list (default) | show <trace-id> | traceparent | services | selftest
Run with -h on any subcommand for its flags.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Dict, List, Optional, Tuple

# --------------------------------------------------------------------------
# Config
# --------------------------------------------------------------------------

DEFAULT_TEMPO_URL = "http://otel:3200"
DEFAULT_OTLP_URL = "http://otel:4318"


def tempo_url() -> str:
    return os.environ.get("TEMPO_URL", DEFAULT_TEMPO_URL).rstrip("/")


def otlp_url() -> str:
    return os.environ.get("OTEL_OTLP_URL", DEFAULT_OTLP_URL).rstrip("/")


# --------------------------------------------------------------------------
# Errors
# --------------------------------------------------------------------------


class BackendUnreachable(Exception):
    """A connection failure talking to Tempo / the OTLP endpoint, or a 5xx
    (Tempo answers 503 while it is still starting)."""

    def __init__(self, url: str, status: Optional[int] = None):
        super().__init__(url)
        self.url = url
        self.status = status


class QueryRejected(Exception):
    """Tempo answered 4xx: it is up, but refused the request — bad TraceQL, an
    invalid regex, a `--last` window over its search limit. Carries Tempo's own
    explanation, trimmed, because that is the only useful part."""

    def __init__(self, url: str, status: int, detail: str):
        super().__init__(f"{status}: {detail}")
        self.url = url
        self.status = status
        self.detail = detail


class TraceNotFound(Exception):
    def __init__(self, trace_id: str):
        super().__init__(trace_id)
        self.trace_id = trace_id


# --------------------------------------------------------------------------
# Durations — parsing (human -> ns) and formatting (ns -> human)
# --------------------------------------------------------------------------

_DURATION_RE = re.compile(r"^(\d+(?:\.\d+)?)(ns|us|ms|s|m|h)$")
_UNIT_NS = {
    "ns": 1,
    "us": 1_000,
    "ms": 1_000_000,
    "s": 1_000_000_000,
    "m": 60_000_000_000,
    "h": 3_600_000_000_000,
}


def parse_duration_ns(raw: str) -> int:
    """Parse a human duration literal (`30s`, `5m`, `2h`, `200ms`, `850us`/`850µs`)
    into integer nanoseconds. Raises ValueError with a clear message."""
    s = raw.strip().replace("µs", "us").replace(" ", "")
    m = _DURATION_RE.match(s)
    if not m:
        raise ValueError(f"invalid duration {raw!r} (expected e.g. 30s, 5m, 2h, 200ms, 850us)")
    value = float(m.group(1))
    unit = m.group(2)
    return int(round(value * _UNIT_NS[unit]))


def normalize_duration_token(raw: str) -> str:
    """Normalize a user duration literal to the ASCII form TraceQL expects
    (µs -> us, no whitespace), validating it in the process."""
    parse_duration_ns(raw)  # validates
    return raw.strip().replace("µs", "us").replace(" ", "")


def _trim(v: float, decimals: int) -> str:
    s = f"{v:.{decimals}f}"
    if "." in s:
        s = s.rstrip("0").rstrip(".")
    return s


def format_duration(ns: Optional[int]) -> str:
    """Render nanoseconds as a short human string: 850µs, 12.4ms, 1.32s."""
    if ns is None:
        return "?"
    ns = abs(int(ns))
    if ns < 1_000:
        return f"{ns}ns"
    if ns < 1_000_000:
        return _trim(ns / 1_000, 1) + "µs"
    if ns < 1_000_000_000:
        return _trim(ns / 1_000_000, 1) + "ms"
    return _trim(ns / 1_000_000_000, 2) + "s"


# --------------------------------------------------------------------------
# Trace/span id normalization — Tempo's search API returns hex ids that are
# sometimes shorter than 32/16 hex chars (a leading zero nibble is dropped),
# while the OTLP JSON embedded in a fetched trace encodes traceId/spanId as
# padded base64. Both need to become a canonical lowercase, zero-padded hex
# string for display and for building the /api/traces/<id> URL.
# --------------------------------------------------------------------------

_HEX_RE = re.compile(r"^[0-9a-fA-F]+$")


def normalize_id(raw: Optional[str], num_bytes: int) -> str:
    s = (raw or "").strip()
    width = num_bytes * 2
    if not s:
        return "0" * width
    # Heuristic: a bare hex string (no '=' padding, no '+'/'/' base64 chars,
    # short enough to be hex) is Tempo's search-API id, possibly missing a
    # leading zero nibble. Anything else is treated as base64 (OTLP JSON
    # id fields), which always carries standard padding in this codebase.
    if _HEX_RE.match(s) and len(s) <= width:
        return s.lower().rjust(width, "0")
    padded = s + "=" * (-len(s) % 4)
    decoded = base64.b64decode(padded)
    return decoded.hex().rjust(width, "0")[:width]


# --------------------------------------------------------------------------
# TraceQL query building (pure)
# --------------------------------------------------------------------------

_KINDS = ("server", "client", "internal", "producer", "consumer")


def _quote_traceql_string(s: str) -> str:
    return s.replace("\\", "\\\\").replace('"', '\\"')


def unanchor(pattern: str) -> str:
    """TraceQL's `=~` is FULLY anchored, so `--name worker` would match only a
    span named exactly "worker". Callers think in substrings (grep habits), so
    pad with `.*` on each side unless the caller anchored that side explicitly
    with `^` / `$` (which TraceQL does not need and which are stripped) or
    already wrote the wildcard."""
    head, tail = ".*", ".*"
    if pattern.startswith("^"):
        pattern, head = pattern[1:], ""
    elif pattern.startswith(".*"):
        head = ""
    if pattern.endswith("$") and not pattern.endswith("\\$"):
        pattern, tail = pattern[:-1], ""
    elif pattern.endswith(".*"):
        tail = ""
    # Alternation binds loosest: `.*GET|POST.*` is `(.*GET)|(POST.*)`, i.e.
    # anchored on both inner ends. Group it so the padding applies to every
    # branch; Tempo's regex engine (Go RE2) accepts the non-capturing form.
    if "|" in pattern and (head or tail):
        pattern = f"(?:{pattern})"
    return f"{head}{pattern}{tail}"


def build_traceql(
    service: Optional[str] = None,
    errors: bool = False,
    min_duration_token: Optional[str] = None,
    name_regex: Optional[str] = None,
    kind: Optional[str] = None,
    root_regex: Optional[str] = None,
    exclude_root_regex: Optional[str] = None,
) -> str:
    """Build a TraceQL query string from list flags. Deterministic predicate
    order so callers/tests can compare the rendered query textually."""
    predicates: List[str] = []
    if service:
        predicates.append(f'resource.service.name="{_quote_traceql_string(service)}"')
    if errors:
        predicates.append("status = error")
    if min_duration_token:
        predicates.append(f"duration > {min_duration_token}")
    if name_regex:
        predicates.append(f'name =~ "{_quote_traceql_string(unanchor(name_regex))}"')
    if kind:
        if kind not in _KINDS:
            raise ValueError(f"invalid kind {kind!r} (expected one of {_KINDS})")
        predicates.append(f"kind = {kind}")
    # Root-span name filters act on the whole trace, unlike `name`, which matches
    # any span in it. `--exclude-root` is the noise gate: periodic background
    # work (one trace per worker tick) otherwise drowns the listing.
    if root_regex:
        predicates.append(f'trace:rootName =~ "{_quote_traceql_string(unanchor(root_regex))}"')
    if exclude_root_regex:
        predicates.append(f'trace:rootName !~ "{_quote_traceql_string(unanchor(exclude_root_regex))}"')
    if not predicates:
        return "{}"
    return "{ " + " && ".join(predicates) + " }"


# --------------------------------------------------------------------------
# HTTP plumbing
# --------------------------------------------------------------------------


def _http_get(url: str, params: Optional[Dict[str, Any]] = None, timeout: float = 8.0) -> Tuple[int, bytes]:
    full = url if not params else url + "?" + urllib.parse.urlencode(params)
    req = urllib.request.Request(full, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except (urllib.error.URLError, OSError) as e:
        raise BackendUnreachable(url) from e


def _http_post_json(url: str, payload: Dict[str, Any], timeout: float = 8.0) -> Tuple[int, bytes]:
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except (urllib.error.URLError, OSError) as e:
        raise BackendUnreachable(url) from e


_DETAIL_MAX = 300


def _error_detail(body: bytes) -> str:
    """Tempo's error body, whitespace-collapsed and capped for one stderr line."""
    text = " ".join(body.decode("utf-8", errors="replace").split())
    if len(text) > _DETAIL_MAX:
        text = text[: _DETAIL_MAX - 1] + "…"
    return text or "(empty response body)"


def _raise_for_status(url: str, status: int, body: bytes) -> None:
    """Map a non-200 Tempo answer: 4xx means Tempo is up and rejected the
    request (QueryRejected, with its explanation); anything else — 5xx, or a
    503 while Tempo is still starting — means it is not serving
    (BackendUnreachable)."""
    if status == 200:
        return
    if 400 <= status < 500:
        raise QueryRejected(url, status, _error_detail(body))
    raise BackendUnreachable(url, status)


# --------------------------------------------------------------------------
# Tempo client
# --------------------------------------------------------------------------


def tempo_search(
    base_url: str,
    query: str,
    start_s: Optional[int] = None,
    end_s: Optional[int] = None,
    limit: int = 20,
) -> Dict[str, Any]:
    params: Dict[str, Any] = {"q": query, "limit": limit}
    if start_s is not None:
        params["start"] = int(start_s)
    if end_s is not None:
        params["end"] = int(end_s)
    status, body = _http_get(f"{base_url}/api/search", params)
    _raise_for_status(base_url, status, body)
    return json.loads(body)


def tempo_get_trace(base_url: str, trace_id: str) -> Dict[str, Any]:
    """Fetch a full trace via the v2 endpoint, which returns the standard
    OTLP TracesData shape (`{"trace": {"resourceSpans": [...]}}`) — the v1
    endpoint (`/api/traces/<id>`) returns the same spans under a legacy
    `{"batches": [...]}` key instead.

    Quirk: for a genuinely unknown id the v1 endpoint answers 404, but v2
    answers 200 with an empty `{"trace":{},"metrics":{}}` body — so an empty
    `resourceSpans` on a 200 is treated the same as a 404 here."""
    status, body = _http_get(f"{base_url}/api/v2/traces/{trace_id}")
    if status == 404:
        raise TraceNotFound(trace_id)
    _raise_for_status(base_url, status, body)
    data = json.loads(body)
    trace = data.get("trace", {}) or {}
    if not trace.get("resourceSpans"):
        raise TraceNotFound(trace_id)
    return trace


def tempo_tag_values(base_url: str, tag: str, start_s: int, end_s: int) -> List[str]:
    """Distinct values of a SCOPED tag (e.g. `resource.service.name`) over a
    time window, via the v2 endpoint.

    start/end are mandatory on purpose: without them Tempo answers from its
    live store only (recent, not-yet-flushed data), so a service whose spans
    all sit in completed blocks — e.g. after the otel container restarted —
    silently drops out while a freshly posted selftest trace shows up. v2
    returns `[{"type": ..., "value": ...}]`; plain strings are accepted too."""
    params = {"start": int(start_s), "end": int(end_s)}
    status, body = _http_get(f"{base_url}/api/v2/search/tag/{tag}/values", params)
    _raise_for_status(base_url, status, body)
    data = json.loads(body)
    values: List[str] = []
    for v in data.get("tagValues", []) or []:
        value = v.get("value") if isinstance(v, dict) else v
        if isinstance(value, str) and value and value not in values:
            values.append(value)
    return values


# --------------------------------------------------------------------------
# `list` line rendering (pure, over a `traces[]` entry from /api/search)
# --------------------------------------------------------------------------


def trace_duration_ns(trace: Dict[str, Any]) -> Optional[int]:
    if "durationMs" in trace:
        return int(trace["durationMs"]) * 1_000_000
    spans = trace.get("spanSet", {}).get("spans", [])
    if spans:
        start = int(trace.get("startTimeUnixNano", spans[0].get("startTimeUnixNano", 0)))
        end = max(int(s["startTimeUnixNano"]) + int(s.get("durationNanos", 0)) for s in spans)
        return max(0, end - start)
    return None


def trace_has_error(trace: Dict[str, Any]) -> bool:
    stats = trace.get("serviceStats", {}) or {}
    return any((v or {}).get("errorCount", 0) > 0 for v in stats.values())


def summarize_trace_line(trace: Dict[str, Any]) -> str:
    tid = normalize_id(trace.get("traceID"), 16)
    start_ns = int(trace.get("startTimeUnixNano", "0") or 0)
    ts = datetime.fromtimestamp(start_ns / 1e9).strftime("%H:%M:%S") if start_ns else "?"
    dur = format_duration(trace_duration_ns(trace))
    svc = trace.get("rootServiceName") or "-"
    name = trace.get("rootTraceName") or "-"
    parts = [tid, ts, dur, svc, name]
    matched = (trace.get("spanSet") or {}).get("matched")
    if matched is not None:
        parts.append(f"matched={matched}")
    if trace_has_error(trace):
        parts.append("ERR")
    return "  ".join(parts)


# --------------------------------------------------------------------------
# `summary` — aggregate query-A/B/C search results by root span name (pure).
#
# summary joins three independent Tempo searches over the same window by
# trace id: the full trace set (root name + duration for every trace, so a
# root with zero db spans still shows up), the db-span subset (`matched`
# gives the db span count per trace), and the error subset (membership only).
# --------------------------------------------------------------------------

_SUMMARY_SORT_KEYS = ("db", "dbmax", "dur", "count", "errors")
_SUMMARY_HINT_DB_AVG_THRESHOLD = 15.0
_SUMMARY_ROOT_DISPLAY_LEN = 48


def build_summary_queries(
    service: Optional[str] = None,
    root_regex: Optional[str] = None,
    exclude_root_regex: Optional[str] = None,
) -> Tuple[str, str, str]:
    """Build the three TraceQL queries `summary` joins (full set / db-span
    subset / error subset), all carrying the same service+root filters so
    the three result sets line up on the same traces."""
    predicates: List[str] = []
    if service:
        predicates.append(f'resource.service.name="{_quote_traceql_string(service)}"')
    if root_regex:
        predicates.append(f'trace:rootName =~ "{_quote_traceql_string(unanchor(root_regex))}"')
    if exclude_root_regex:
        predicates.append(f'trace:rootName !~ "{_quote_traceql_string(unanchor(exclude_root_regex))}"')

    def render(extra: Optional[str]) -> str:
        preds = predicates + [extra] if extra else predicates
        return "{ " + " && ".join(preds) + " }" if preds else "{}"

    # Mirrors is_db_query_span, in TraceQL: db.system(.name) marks a db span
    # (db.system pre-semconv-1.40 SQLAlchemy/redis instrumentations,
    # db.system.name >= 1.40 Go otelpgx/Node — mutually exclusive per span,
    # either presence qualifies), but a connection-lifecycle span like pgx's
    # `pool.acquire` carries db.system(.name) too with no statement/operation
    # of its own — requiring one of db.statement/db.query.text/db.operation/
    # db.operation.name is what excludes pool sizing from the db count.
    db_predicate = (
        "(span.db.system != nil || span.db.system.name != nil)"
        " && (span.db.statement != nil || span.db.query.text != nil"
        " || span.db.operation != nil || span.db.operation.name != nil)"
    )
    return render(None), render(db_predicate), render("status = error")


def detect_truncated_queries(limit: int, named_results: List[Tuple[str, List[Dict[str, Any]]]]) -> List[str]:
    """Which of the three searches hit `limit` results exactly — those
    windows were truncated and the aggregation over them is a sample."""
    return [name for name, traces in named_results if len(traces) >= limit]


@dataclass
class SummaryRow:
    root: str
    trace_count: int = 0
    db_total: int = 0
    db_max: int = 0
    dur_total_ns: int = 0
    dur_max_ns: int = 0
    error_count: int = 0

    @property
    def db_avg(self) -> float:
        return self.db_total / self.trace_count if self.trace_count else 0.0

    @property
    def dur_avg_ns(self) -> int:
        return self.dur_total_ns // self.trace_count if self.trace_count else 0


def build_summary_rows(
    all_traces: List[Dict[str, Any]],
    db_traces: List[Dict[str, Any]],
    error_traces: List[Dict[str, Any]],
) -> List[SummaryRow]:
    """Three-way join by trace id -> one aggregated row per root span name."""
    db_by_id: Dict[str, int] = {}
    for t in db_traces:
        tid = normalize_id(t.get("traceID"), 16)
        matched = (t.get("spanSet") or {}).get("matched")
        db_by_id[tid] = int(matched) if matched is not None else db_by_id.get(tid, 0)
    error_ids = {normalize_id(t.get("traceID"), 16) for t in error_traces}

    # `all_traces` (query B) is supposed to be exhaustive, but a trace seen
    # only by the db/error query is still counted rather than dropped.
    merged: Dict[str, Dict[str, Any]] = {}
    order: List[str] = []
    for traces in (all_traces, db_traces, error_traces):
        for t in traces:
            tid = normalize_id(t.get("traceID"), 16)
            if tid not in merged:
                merged[tid] = t
                order.append(tid)

    rows: Dict[str, SummaryRow] = {}
    row_order: List[str] = []
    for tid in order:
        t = merged[tid]
        root = t.get("rootTraceName") or "-"
        dur_ns = trace_duration_ns(t) or 0
        db = db_by_id.get(tid, 0)
        if root not in rows:
            rows[root] = SummaryRow(root=root)
            row_order.append(root)
        r = rows[root]
        r.trace_count += 1
        r.db_total += db
        r.db_max = max(r.db_max, db)
        r.dur_total_ns += dur_ns
        r.dur_max_ns = max(r.dur_max_ns, dur_ns)
        if tid in error_ids:
            r.error_count += 1
    return [rows[root] for root in row_order]


def sort_summary_rows(rows: List[SummaryRow], sort: str) -> List[SummaryRow]:
    if sort not in _SUMMARY_SORT_KEYS:
        raise ValueError(f"invalid sort {sort!r} (expected one of {_SUMMARY_SORT_KEYS})")
    key_fn = {
        "db": lambda r: r.db_total,
        "dbmax": lambda r: r.db_max,
        "dur": lambda r: r.dur_avg_ns,
        "count": lambda r: r.trace_count,
        "errors": lambda r: r.error_count,
    }[sort]
    return sorted(rows, key=lambda r: (-key_fn(r), r.root))


def render_summary_table(rows: List[SummaryRow]) -> List[str]:
    headers = ["root", "traces", "db/trace avg", "max", "db total", "dur avg", "max", "errors"]
    body: List[List[str]] = []
    for r in rows:
        body.append(
            [
                collapse_and_truncate(r.root, limit=_SUMMARY_ROOT_DISPLAY_LEN),
                str(r.trace_count),
                f"{r.db_avg:.1f}",
                str(r.db_max),
                str(r.db_total),
                format_duration(r.dur_avg_ns),
                format_duration(r.dur_max_ns),
                str(r.error_count),
            ]
        )
    widths = [len(h) for h in headers]
    for row in body:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], len(cell))

    def fmt_row(cells: List[str]) -> str:
        return "  ".join(c.ljust(widths[i]) if i == 0 else c.rjust(widths[i]) for i, c in enumerate(cells)).rstrip()

    return [fmt_row(headers)] + [fmt_row(row) for row in body]


def summary_hint_line(rows: List[SummaryRow]) -> Optional[str]:
    """Point at the roots that look like a query fan-out: db spans per trace at
    or above the threshold, worst first, three at most. Several, not one: a
    legitimately chatty periodic job and a regressed endpoint both cross the
    threshold, and naming only the worst would hide the other. The unnamed
    `-` row is skipped — it collects traces whose root span has a remote parent
    (e.g. requests sent with a caller-made `traceparent`), so there is no root
    name to filter by."""
    candidates = [
        r
        for r in rows
        if r.trace_count and r.root.strip("-") and r.db_avg >= _SUMMARY_HINT_DB_AVG_THRESHOLD
    ]
    if not candidates:
        return None
    candidates.sort(key=lambda r: r.db_avg, reverse=True)
    lines = []
    for row in candidates[:3]:
        pattern = "^" + re.escape(row.root) + "$"
        lines.append(
            f'hint: "{row.root}" averages {row.db_avg:.1f} db spans per trace ({row.trace_count} traces) — '
            f"inspect one: list --root '{pattern}' --limit 1, then show <id>"
        )
    return "\n".join(lines)


# --------------------------------------------------------------------------
# OTLP span model + tree building
# --------------------------------------------------------------------------


def extract_attr_value(v: Dict[str, Any]) -> Any:
    if "stringValue" in v:
        return v["stringValue"]
    if "intValue" in v:
        try:
            return int(v["intValue"])
        except (TypeError, ValueError):
            return v["intValue"]
    if "boolValue" in v:
        return bool(v["boolValue"])
    if "doubleValue" in v:
        return float(v["doubleValue"])
    if "arrayValue" in v:
        return [extract_attr_value(x) for x in v["arrayValue"].get("values", [])]
    if "kvlistValue" in v:
        return {kv["key"]: extract_attr_value(kv.get("value", {})) for kv in v["kvlistValue"].get("values", [])}
    if "bytesValue" in v:
        return v["bytesValue"]
    return None


def flatten_attributes(attr_list: Optional[List[Dict[str, Any]]]) -> Dict[str, Any]:
    out: Dict[str, Any] = {}
    for a in attr_list or []:
        out[a.get("key", "")] = extract_attr_value(a.get("value", {}) or {})
    return out


_KIND_MAP = {
    "SPAN_KIND_UNSPECIFIED": "unspecified",
    "SPAN_KIND_INTERNAL": "internal",
    "SPAN_KIND_SERVER": "server",
    "SPAN_KIND_CLIENT": "client",
    "SPAN_KIND_PRODUCER": "producer",
    "SPAN_KIND_CONSUMER": "consumer",
    0: "unspecified",
    1: "internal",
    2: "server",
    3: "client",
    4: "producer",
    5: "consumer",
}

_STATUS_MAP = {
    "STATUS_CODE_UNSET": "unset",
    "STATUS_CODE_OK": "ok",
    "STATUS_CODE_ERROR": "error",
    0: "unset",
    1: "ok",
    2: "error",
}


@dataclass
class SpanEvent:
    name: str
    time_ns: int
    attributes: Dict[str, Any]


@dataclass
class Span:
    trace_id: str
    span_id: str
    parent_id: Optional[str]
    name: str
    kind: str
    start_ns: int
    end_ns: int
    service: str
    attributes: Dict[str, Any]
    status_code: str
    status_message: str
    events: List[SpanEvent] = field(default_factory=list)


def flatten_trace(trace: Dict[str, Any]) -> List[Span]:
    """Turn an OTLP TracesData dict (`{"resourceSpans": [...]}`) into a flat
    list of Span records with hex ids and normalized kind/status."""
    spans: List[Span] = []
    for rs in trace.get("resourceSpans", []) or []:
        resource_attrs = flatten_attributes((rs.get("resource") or {}).get("attributes"))
        service = resource_attrs.get("service.name", "?")
        for ss in rs.get("scopeSpans", []) or []:
            for raw in ss.get("spans", []) or []:
                trace_id = normalize_id(raw.get("traceId"), 16)
                span_id = normalize_id(raw.get("spanId"), 8)
                parent_raw = raw.get("parentSpanId")
                parent_id = normalize_id(parent_raw, 8) if parent_raw else None
                start_ns = int(raw.get("startTimeUnixNano", 0) or 0)
                end_ns = int(raw.get("endTimeUnixNano", start_ns) or start_ns)
                status = raw.get("status", {}) or {}
                status_code = _STATUS_MAP.get(status.get("code"), "unset")
                events = [
                    SpanEvent(
                        name=ev.get("name", ""),
                        time_ns=int(ev.get("timeUnixNano", 0) or 0),
                        attributes=flatten_attributes(ev.get("attributes")),
                    )
                    for ev in raw.get("events", []) or []
                ]
                spans.append(
                    Span(
                        trace_id=trace_id,
                        span_id=span_id,
                        parent_id=parent_id,
                        name=raw.get("name", ""),
                        kind=_KIND_MAP.get(raw.get("kind"), "unspecified"),
                        start_ns=start_ns,
                        end_ns=end_ns,
                        service=service,
                        attributes=flatten_attributes(raw.get("attributes")),
                        status_code=status_code,
                        status_message=status.get("message", ""),
                        events=events,
                    )
                )
    return spans


@dataclass
class Node:
    span: Span
    children: List["Node"] = field(default_factory=list)
    orphan: bool = False


def build_tree(spans: List[Span]) -> List[Node]:
    by_id: Dict[str, Node] = {s.span_id: Node(s) for s in spans}
    roots: List[Node] = []
    for s in spans:
        node = by_id[s.span_id]
        parent = by_id.get(s.parent_id) if s.parent_id else None
        if parent is not None:
            parent.children.append(node)
        else:
            if s.parent_id:
                node.orphan = True
            roots.append(node)

    def sort_rec(n: Node) -> None:
        n.children.sort(key=lambda c: c.span.start_ns)
        for c in n.children:
            sort_rec(c)

    for r in roots:
        sort_rec(r)
    roots.sort(key=lambda n: n.span.start_ns)
    return roots


def self_time_ns(node: Node) -> int:
    dur = node.span.end_ns - node.span.start_ns
    children_dur = sum(c.span.end_ns - c.span.start_ns for c in node.children)
    return max(0, dur - children_dur)


def collect_self_times(roots: List[Node]) -> List[Tuple[Node, int]]:
    out: List[Tuple[Node, int]] = []

    def walk(n: Node) -> None:
        out.append((n, self_time_ns(n)))
        for c in n.children:
            walk(c)

    for r in roots:
        walk(r)
    return out


# --------------------------------------------------------------------------
# Statement normalization + folding (pure)
# --------------------------------------------------------------------------

_WS_RE = re.compile(r"\s+")
_QUOTED_RE = re.compile(r"'[^']*'")
_PLACEHOLDER_RE = re.compile(r"\$\d+")
_IN_LIST_RE = re.compile(r"(?i)\bIN\s*\([^)]*\)")
_NUMBER_RE = re.compile(r"(?<![A-Za-z0-9_])\d+(\.\d+)?(?![A-Za-z0-9_])")
_SQL_LINE_COMMENT_RE = re.compile(r"^\s*--[^\n]*\n?")
_SQL_BLOCK_COMMENT_RE = re.compile(r"^\s*/\*.*?\*/\s*", re.DOTALL)
_SQLC_NAME_RE = re.compile(r"--\s*name:\s*(\w+)")


def strip_sql_comments(stmt: str) -> str:
    """Strip leading `--` line comments and `/* */` block comments. sqlc
    prepends a `-- name: GetEntries :many` header to every generated query,
    which otelpgx also surfaces verbatim as db.statement/db.query.text (and,
    per-span, as the span's own name) — fold and display should ignore it."""
    s = stmt
    while True:
        m = _SQL_LINE_COMMENT_RE.match(s)
        if m:
            s = s[m.end():]
            continue
        m = _SQL_BLOCK_COMMENT_RE.match(s)
        if m:
            s = s[m.end():]
            continue
        break
    return s.lstrip()


def sqlc_name_of(text: str) -> Optional[str]:
    """Extract the query name from a leading sqlc header comment
    (`-- name: GetEntries :many`), if `text` starts with one."""
    m = _SQLC_NAME_RE.match(text.strip())
    return m.group(1) if m else None


def normalize_statement(stmt: str) -> str:
    """Fold away literal values so structurally-identical statements compare
    equal: sqlc header comment stripped, whitespace collapsed, quoted
    strings/numeric literals/`$n` placeholders/`IN (...)` lists all become
    `?`."""
    s = strip_sql_comments(stmt)
    s = _WS_RE.sub(" ", s.strip())
    s = _QUOTED_RE.sub("?", s)
    s = _PLACEHOLDER_RE.sub("?", s)
    s = _IN_LIST_RE.sub("IN (?)", s)
    s = _NUMBER_RE.sub("?", s)
    return s


def statement_of(span: Span) -> Optional[str]:
    return span.attributes.get("db.statement") or span.attributes.get("db.query.text")


def is_db_span(attrs: Dict[str, Any]) -> bool:
    """True for a db span under either the old (`db.system`) or new
    (`db.system.name`, semconv >= 1.40, e.g. Go otelpgx 0.12+) key."""
    return "db.system" in attrs or "db.system.name" in attrs


def is_db_query_span(attrs: Dict[str, Any]) -> bool:
    """True for a db span that names an actual statement or operation —
    narrower than `is_db_span`, which also matches connection-lifecycle
    spans like pgx's `pool.acquire`: otelpgx tags those with
    `db.system(.name)` too, but they carry no `db.statement`/`db.operation`
    of their own, so a burst of them is pool sizing, not a query loop."""
    if not is_db_span(attrs):
        return False
    return bool(
        attrs.get("db.statement")
        or attrs.get("db.query.text")
        or attrs.get("db.operation")
        or attrs.get("db.operation.name")
    )


def db_system_of(attrs: Dict[str, Any]) -> Optional[str]:
    return attrs.get("db.system") or attrs.get("db.system.name")


def db_operation_of(attrs: Dict[str, Any]) -> Optional[str]:
    return attrs.get("db.operation") or attrs.get("db.operation.name")


def fold_key(span: Span) -> Tuple[str, Optional[str], str]:
    # The status is part of the key so an errored sibling never folds into a
    # "×N" line: it renders on its own, with its ERROR and exception lines.
    stmt = statement_of(span)
    return (span.name, normalize_statement(stmt) if stmt else None, span.status_code)


@dataclass
class FoldGroup:
    nodes: List[Node]

    @property
    def count(self) -> int:
        return len(self.nodes)

    @property
    def name(self) -> str:
        return self.nodes[0].span.name

    @property
    def is_db(self) -> bool:
        return is_db_query_span(self.nodes[0].span.attributes)

    @property
    def durations_ns(self) -> List[int]:
        return [n.span.end_ns - n.span.start_ns for n in self.nodes]


def group_siblings_for_fold(nodes: List[Node]) -> List[List[Node]]:
    order: List[Tuple[str, Optional[str]]] = []
    buckets: Dict[Tuple[str, Optional[str]], List[Node]] = {}
    for n in nodes:
        key = fold_key(n.span)
        if key not in buckets:
            buckets[key] = []
            order.append(key)
        buckets[key].append(n)
    return [buckets[k] for k in order]


# --------------------------------------------------------------------------
# `show` rendering (pure over already-fetched spans)
# --------------------------------------------------------------------------

_TRUNCATE_LEN = 140


def collapse_and_truncate(s: str, limit: int = _TRUNCATE_LEN) -> str:
    s = _WS_RE.sub(" ", s.strip())
    if len(s) > limit:
        s = s[: limit - 1].rstrip() + "…"
    return s


def http_summary(span: Span) -> Optional[str]:
    a = span.attributes
    method = a.get("http.request.method") or a.get("http.method")
    if not method and "http.route" not in a and "http.target" not in a and "url.path" not in a:
        return None
    route = a.get("http.route") or a.get("url.path") or a.get("http.target")
    status = a.get("http.response.status_code") or a.get("http.status_code")
    bits = [b for b in [method, route] if b]
    text = " ".join(str(b) for b in bits)
    if status is not None:
        text += f" -> {status}"
    return text or None


def format_statement_summary(stmt: str, normalize: bool = False, limit: int = _TRUNCATE_LEN) -> str:
    """Statement text for display: comment-stripped (optionally normalized
    to fold away literals), truncated, with a leading sqlc query name shown
    as a prefix when the statement carries a `-- name: X` header comment
    (`GetEntries: SELECT …`)."""
    name = sqlc_name_of(stmt)
    cleaned = normalize_statement(stmt) if normalize else strip_sql_comments(stmt)
    text = collapse_and_truncate(cleaned, limit=limit)
    return f"{name}: {text}" if name else text


def display_span_name(span: Span) -> str:
    """The span's display name. otelpgx (and similar sqlc-aware
    instrumentations) name a span after the first line of the query text,
    which for a sqlc-generated query is a `-- name: X :many` header comment
    rather than useful text — fall back to the sqlc query name, or a
    comment-stripped statement snippet, rather than printing the raw `--`."""
    raw = span.name
    if raw and not raw.strip().startswith("--"):
        return raw
    name = sqlc_name_of(raw or "")
    if name:
        return name
    stmt = statement_of(span)
    if stmt:
        name = sqlc_name_of(stmt)
        if name:
            return name
        cleaned = strip_sql_comments(stmt)
        if cleaned:
            return collapse_and_truncate(cleaned, limit=40)
    return raw or "?"


def db_summary(span: Span) -> Optional[str]:
    if not is_db_span(span.attributes):
        return None
    stmt = statement_of(span)
    if stmt:
        return format_statement_summary(stmt)
    op = db_operation_of(span.attributes)
    if op:
        return str(op)
    return None


def pick_stacktrace_lines(stacktrace: str, limit: int = 5) -> List[str]:
    lines = [l for l in stacktrace.splitlines() if l.strip()]
    app_lines = [l for l in lines if "/workspace/src/" in l]
    if app_lines:
        return app_lines[:limit]
    return lines[-limit:]


def render_span_attrs(span: Span) -> Optional[str]:
    """Pick the few attributes that matter for this span's type."""
    text = http_summary(span)
    if text is not None:
        return text
    text = db_summary(span)
    if text is not None:
        return text
    return None


def render_error_lines(span: Span, indent: str) -> List[str]:
    lines: List[str] = []
    if span.status_code != "error":
        return lines
    msg = span.status_message or "(no message)"
    lines.append(f"{indent}  ERROR {msg}")
    for ev in span.events:
        if ev.name != "exception":
            continue
        etype = ev.attributes.get("exception.type", "?")
        emsg = ev.attributes.get("exception.message", "")
        lines.append(f"{indent}  {etype}: {emsg}")
        trace = ev.attributes.get("exception.stacktrace")
        if trace:
            for tl in pick_stacktrace_lines(str(trace)):
                lines.append(f"{indent}    {tl.strip()}")
    return lines


def render_span_unit(node: Node, depth: int, trace_start: int) -> List[str]:
    span = node.span
    indent = "  " * depth
    offset = format_duration(span.start_ns - trace_start)
    dur = format_duration(span.end_ns - span.start_ns)
    tag = f"[{span.kind}]"
    if node.orphan:
        # A root whose span carries a parent_id Tempo never saw — a caller-made
        # traceparent header, not a broken trace: the parent lives in whatever
        # sent the request. "orphan" reads as data loss; it isn't one.
        tag += " (remote parent)"
    attrs = render_span_attrs(span)
    line = f"{indent}+{offset}  {dur}  {display_span_name(span)} {tag}"
    if attrs:
        line += f"  {attrs}"
    lines = [line]
    lines.extend(render_error_lines(span, indent))
    return lines


def render_fold_unit(group: List[Node], depth: int, trace_start: int) -> List[str]:
    indent = "  " * depth
    durations = [n.span.end_ns - n.span.start_ns for n in group]
    total = sum(durations)
    avg = total // len(durations)
    mx = max(durations)
    name = display_span_name(group[0].span)
    stmt = statement_of(group[0].span)
    # The NORMALIZED statement, not the first member's: a fold stands for N
    # different literals, so printing one of them (`item_id = 100`) misleads.
    text = format_statement_summary(stmt, normalize=True) if stmt else ""
    is_query = is_db_query_span(group[0].span.attributes)
    # The N+1 hint is a query-fan-out call to action: a repeated
    # connection-lifecycle span (e.g. pgx `pool.acquire`, is_db_span but not
    # is_db_query_span) isn't a query loop, just pool sizing — fold it silently.
    suffix = " <- possible N+1" if (is_query and len(group) >= 5) else ""
    line = (
        f"{indent}×{len(group)}  total={format_duration(total)}  avg={format_duration(avg)}  "
        f"max={format_duration(mx)}"
    )
    # SQL spans are named after their verb, so `SELECT SELECT * FROM …` would
    # just repeat it; the statement alone is enough there.
    if not (text and text.upper().startswith(name.upper())):
        line += f"  {name}"
    if text:
        line += f"  {text}"
    line += suffix
    return [line]


@dataclass
class RenderUnit:
    depth: int
    lines: List[str]
    span_count: int


def flatten_render_units(roots: List[Node], trace_start: int, full: bool) -> List[RenderUnit]:
    units: List[RenderUnit] = []

    def walk(nodes: List[Node], depth: int) -> None:
        groups = [[n] for n in nodes] if full else group_siblings_for_fold(nodes)
        for group in groups:
            if len(group) >= 2:
                units.append(RenderUnit(depth, render_fold_unit(group, depth, trace_start), len(group)))
                # Children of a folded group are summarized, not expanded.
            else:
                node = group[0]
                units.append(RenderUnit(depth, render_span_unit(node, depth, trace_start), 1))
                walk(node.children, depth + 1)

    walk(roots, 0)
    return units


def render_tree(roots: List[Node], trace_start: int, full: bool, max_spans: int) -> List[str]:
    units = flatten_render_units(roots, trace_start, full)
    out: List[str] = []
    shown_spans = 0
    for i, u in enumerate(units):
        if i >= max_spans:
            remaining_spans = sum(x.span_count for x in units[i:])
            out.append(f"… {remaining_spans} more spans")
            break
        out.extend(u.lines)
        shown_spans += u.span_count
    return out


def render_show(trace_id: str, spans: List[Span], full: bool = False, max_spans: int = 200) -> str:
    if not spans:
        return f"trace {trace_id}: no spans returned"
    roots = build_tree(spans)
    trace_start = min(s.start_ns for s in spans)
    trace_end = max(s.end_ns for s in spans)
    services = sorted(set(s.service for s in spans))
    error_spans = [s for s in spans if s.status_code == "error"]
    db_spans = [s for s in spans if is_db_span(s.attributes)]
    db_time = sum(s.end_ns - s.start_ns for s in db_spans)
    self_times = collect_self_times(roots)
    top3 = sorted(self_times, key=lambda kv: -kv[1])[:3]

    lines = [
        f"trace {trace_id}",
        f"  start={datetime.fromtimestamp(trace_start / 1e9).strftime('%Y-%m-%d %H:%M:%S')}"
        f"  duration={format_duration(trace_end - trace_start)}  spans={len(spans)}",
        f"  services: {', '.join(services)}",
        f"  errors: {len(error_spans)}   db: {len(db_spans)} spans, {format_duration(db_time)} total",
    ]
    if top3:
        top_str = "; ".join(f"{display_span_name(n.span)} {format_duration(ns)}" for n, ns in top3)
        lines.append(f"  top self-time: {top_str}")
    lines.append("")
    lines.extend(render_tree(roots, trace_start, full, max_spans))
    return "\n".join(lines)


# --------------------------------------------------------------------------
# traceparent generation
# --------------------------------------------------------------------------


def make_traceparent() -> Tuple[str, str]:
    trace_id = os.urandom(16).hex()
    span_id = os.urandom(8).hex()
    return f"00-{trace_id}-{span_id}-01", trace_id


# --------------------------------------------------------------------------
# CLI subcommands (I/O)
# --------------------------------------------------------------------------


def _err(msg: str) -> None:
    print(msg, file=sys.stderr)


def cmd_list(args: argparse.Namespace) -> int:
    base = tempo_url()
    last_ns = parse_duration_ns(args.last)
    end_s = int(time.time())
    start_s = end_s - int(round(last_ns / 1e9))
    if args.raw_query:
        query = args.raw_query
    else:
        min_dur_token = normalize_duration_token(args.min_duration) if args.min_duration else None
        query = build_traceql(
            service=args.service,
            errors=args.errors,
            min_duration_token=min_dur_token,
            name_regex=args.name_regex,
            kind=args.kind,
            root_regex=args.root_regex,
            exclude_root_regex=args.exclude_root_regex,
        )
    start_local = datetime.fromtimestamp(start_s).strftime("%H:%M:%S")
    end_local = datetime.fromtimestamp(end_s).strftime("%H:%M:%S")
    print(f"query: {query}  window: last={args.last} ({start_local} .. {end_local})")

    data = tempo_search(base, query, start_s, end_s, args.limit)
    traces = data.get("traces", []) or []
    if not traces:
        print("no traces matched")
        return 0
    for t in traces:
        print(summarize_trace_line(t))
    return 0


def cmd_summary(args: argparse.Namespace) -> int:
    base = tempo_url()
    last_ns = parse_duration_ns(args.last)
    end_s = int(time.time())
    start_s = end_s - int(round(last_ns / 1e9))
    q_all, q_db, q_err = build_summary_queries(args.service, args.root_regex, args.exclude_root_regex)

    all_data = tempo_search(base, q_all, start_s, end_s, args.limit)
    db_data = tempo_search(base, q_db, start_s, end_s, args.limit)
    err_data = tempo_search(base, q_err, start_s, end_s, args.limit)
    all_traces = all_data.get("traces", []) or []
    db_traces = db_data.get("traces", []) or []
    err_traces = err_data.get("traces", []) or []

    start_local = datetime.fromtimestamp(start_s).strftime("%H:%M:%S")
    end_local = datetime.fromtimestamp(end_s).strftime("%H:%M:%S")
    print(f"window: last={args.last} ({start_local} .. {end_local})  traces seen: {len(all_traces)}")
    truncated = detect_truncated_queries(
        args.limit, [("all", all_traces), ("db", db_traces), ("errors", err_traces)]
    )
    if truncated:
        print(f"warning: {', '.join(truncated)} query hit --limit {args.limit} — numbers are a sample, not the full window")

    rows = build_summary_rows(all_traces, db_traces, err_traces)
    if not rows:
        print("no traces matched")
        return 0
    rows = sort_summary_rows(rows, args.sort)[: args.top]
    for line in render_summary_table(rows):
        print(line)
    hint = summary_hint_line(rows)
    if hint:
        print(hint)
    return 0


def cmd_show(args: argparse.Namespace) -> int:
    base = tempo_url()
    trace_id_in = args.trace_id.strip().lower()
    trace = tempo_get_trace(base, trace_id_in)
    if args.json:
        print(json.dumps(trace, indent=2))
        return 0
    spans = flatten_trace(trace)
    if not spans:
        print(f"trace {trace_id_in}: no spans returned")
        return 0
    trace_id = spans[0].trace_id
    print(render_show(trace_id, spans, full=args.full, max_spans=args.max_spans))
    return 0


def cmd_traceparent(_args: argparse.Namespace) -> int:
    header, trace_id = make_traceparent()
    print(f"traceparent: {header}")
    print(f"trace_id: {trace_id}")
    return 0


def cmd_services(args: argparse.Namespace) -> int:
    base = tempo_url()
    last_ns = parse_duration_ns(args.last)
    end_s = int(time.time())
    start_s = end_s - int(round(last_ns / 1e9))
    values = tempo_tag_values(base, "resource.service.name", start_s, end_s)
    if not values:
        print(f"no services seen by Tempo in the last {args.last}")
        return 0
    for v in sorted(values):
        print(v)
    return 0


# --------------------------------------------------------------------------
# selftest — post a synthetic trace, poll for it, render it, assert on the
# rendering (fold + N+1 marker + exception line).
# --------------------------------------------------------------------------


def _attr(key: str, value: Any, vtype: str = "stringValue") -> Dict[str, Any]:
    return {"key": key, "value": {vtype: value}}


def build_selftest_payload() -> Tuple[Dict[str, Any], str]:
    trace_id = os.urandom(16).hex()
    root_id = os.urandom(8).hex()
    list_select_id = os.urandom(8).hex()
    redis_id = os.urandom(8).hex()
    error_id = os.urandom(8).hex()
    n1_ids = [os.urandom(8).hex() for _ in range(12)]

    now_ns = int(time.time() * 1e9)
    t = now_ns

    def span(
        span_id: str,
        parent_id: Optional[str],
        name: str,
        kind: str,
        start: int,
        dur_ns: int,
        attrs: List[Dict[str, Any]],
        status: Optional[Dict[str, Any]] = None,
        events: Optional[List[Dict[str, Any]]] = None,
    ) -> Dict[str, Any]:
        d: Dict[str, Any] = {
            "traceId": trace_id,
            "spanId": span_id,
            "name": name,
            "kind": kind,
            "startTimeUnixNano": str(start),
            "endTimeUnixNano": str(start + dur_ns),
            "attributes": attrs,
        }
        if parent_id:
            d["parentSpanId"] = parent_id
        if status:
            d["status"] = status
        if events:
            d["events"] = events
        return d

    spans = []
    root_start = t
    root_dur = 20_000_000  # 20ms
    spans.append(
        span(
            root_id,
            None,
            "GET /selftest/items",
            "SPAN_KIND_SERVER",
            root_start,
            root_dur,
            [
                _attr("http.method", "GET"),
                _attr("http.route", "/selftest/items"),
                _attr("http.status_code", 200, "intValue"),
            ],
            status={"code": "STATUS_CODE_OK"},
        )
    )

    cursor = root_start + 1_000_000
    spans.append(
        span(
            list_select_id,
            root_id,
            "SELECT",
            "SPAN_KIND_CLIENT",
            cursor,
            1_500_000,
            [
                _attr("db.system", "postgresql"),
                _attr("db.name", "ficbird"),
                _attr("db.statement", "SELECT id, name FROM items"),
            ],
        )
    )
    cursor += 1_500_000 + 200_000

    for i, sid in enumerate(n1_ids):
        dur = 1_000_000 + i * 20_000
        spans.append(
            span(
                sid,
                root_id,
                "SELECT",
                "SPAN_KIND_CLIENT",
                cursor,
                dur,
                [
                    _attr("db.system", "postgresql"),
                    _attr("db.name", "ficbird"),
                    _attr("db.statement", f"SELECT * FROM items WHERE item_id = {100 + i}"),
                ],
            )
        )
        cursor += dur + 100_000

    spans.append(
        span(
            redis_id,
            root_id,
            "GET",
            "SPAN_KIND_CLIENT",
            cursor,
            300_000,
            [_attr("db.system", "redis"), _attr("db.statement", "GET item:cache:selftest")],
        )
    )
    cursor += 300_000 + 100_000

    exc_start = cursor
    exc_dur = 500_000
    spans.append(
        span(
            error_id,
            root_id,
            "enrich_item",
            "SPAN_KIND_INTERNAL",
            exc_start,
            exc_dur,
            [],
            status={"code": "STATUS_CODE_ERROR", "message": "enrichment failed"},
            events=[
                {
                    "timeUnixNano": str(exc_start + 100_000),
                    "name": "exception",
                    "attributes": [
                        _attr("exception.type", "ValueError"),
                        _attr("exception.message", "unexpected null field 'tags'"),
                        _attr(
                            "exception.stacktrace",
                            "Traceback (most recent call last):\n"
                            '  File "/workspace/src/app/api/items.py", line 42, in enrich_item\n'
                            "    tags = item[\"tags\"]\n"
                            "KeyError: 'tags'\n"
                            "ValueError: unexpected null field 'tags'",
                        ),
                    ],
                }
            ],
        )
    )

    payload = {
        "resourceSpans": [
            {
                "resource": {"attributes": [_attr("service.name", "traces-selftest")]},
                "scopeSpans": [{"scope": {"name": "traces-selftest"}, "spans": spans}],
            }
        ]
    }
    return payload, trace_id


def cmd_selftest(_args: argparse.Namespace) -> int:
    base = tempo_url()
    otlp = otlp_url()
    payload, trace_id = build_selftest_payload()

    status, body = _http_post_json(f"{otlp}/v1/traces", payload)
    if status not in (200, 202):
        print(f"selftest: FAILED: ingest returned status {status}: {body[:200]!r}", file=sys.stderr)
        return 1

    trace: Dict[str, Any] = {}
    found = False
    deadline = time.time() + 15
    while time.time() < deadline:
        try:
            # tempo_get_trace raises TraceNotFound both for a genuinely
            # unknown id and for one not yet flushed from the WAL — either
            # way, keep polling until the deadline.
            trace = tempo_get_trace(base, trace_id)
            found = True
            break
        except TraceNotFound:
            time.sleep(1)
    if not found:
        print(f"selftest: FAILED: trace {trace_id} not queryable in Tempo after 15s", file=sys.stderr)
        return 1

    spans = flatten_trace(trace)
    text = render_show(trace_id, spans, full=False, max_spans=200)
    print(text)

    problems = []
    if "×12" not in text:
        problems.append("missing '×12' fold line")
    if "possible N+1" not in text:
        problems.append("missing 'possible N+1' marker")
    if "ValueError: unexpected null field" not in text:
        problems.append("missing exception line")
    if problems:
        print(f"selftest: FAILED: {'; '.join(problems)}", file=sys.stderr)
        return 1
    print("selftest: OK")
    return 0


# --------------------------------------------------------------------------
# argparse wiring
# --------------------------------------------------------------------------

SUBCOMMANDS = ("summary", "list", "show", "traceparent", "services", "selftest")

# Exit codes. 2 is shared with argparse's own usage errors on purpose: both
# mean "fix the invocation"; 3 means Tempo is up but refused the request.
EXIT_NOT_FOUND = 1  # trace id unknown to Tempo; selftest failure
EXIT_USAGE = 2  # invalid flag value (argparse usage errors exit 2 too)
EXIT_UNREACHABLE = 2  # no connection, or Tempo answered 5xx (503 while starting)
EXIT_QUERY_REJECTED = 3  # Tempo answered 4xx: bad TraceQL / regex / window; its reason is printed

_EXIT_CODES_HELP = """exit codes:
  0  success
  1  trace not found; selftest failed
  2  invalid arguments, or the otel backend is unreachable (no connection, HTTP 5xx)
  3  Tempo rejected the query (HTTP 4xx: bad TraceQL, invalid regex, window too
     long); Tempo's reason is printed on stderr
"""


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="traces.py",
        description=(
            "Compact text view over Tempo traces for an AI agent or a human in a terminal. "
            "Start with `summary` to see where the load, slowness, or errors are, "
            "then drill in with `list` and `show`."
        ),
        epilog=_EXIT_CODES_HELP,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    sub = parser.add_subparsers(dest="subcommand")

    p_summary = sub.add_parser(
        "summary", help="Aggregate recent traces by root span name — where is DB load/slowness/errors coming from"
    )
    p_summary.add_argument("--last", default="15m", help="time window, e.g. 30s, 5m, 2h (default 15m)")
    p_summary.add_argument("--service", help="filter by resource.service.name")
    p_summary.add_argument("--root", dest="root_regex", help="regex over the ROOT span's name (substring match; anchor with ^ / $)")
    p_summary.add_argument(
        "--exclude-root", dest="exclude_root_regex", help="drop traces whose ROOT span name matches, e.g. '^worker '"
    )
    p_summary.add_argument("--limit", type=int, default=500, help="per-query Tempo search result cap (default 500)")
    p_summary.add_argument(
        "--sort",
        choices=list(_SUMMARY_SORT_KEYS),
        default="db",
        help="sort rows by (default db = total db spans contributed, the biggest DB-load contributor first)",
    )
    p_summary.add_argument("--top", type=int, default=20, help="max rows to print (default 20)")
    p_summary.set_defaults(func=cmd_summary)

    p_list = sub.add_parser("list", help="List recent traces matching filters (default subcommand)")
    p_list.add_argument("--last", default="15m", help="time window, e.g. 30s, 5m, 2h (default 15m)")
    p_list.add_argument("--service", help="filter by resource.service.name")
    p_list.add_argument("--errors", action="store_true", help="only traces with an error span")
    p_list.add_argument("--min-duration", dest="min_duration", help="e.g. 200ms, 1s")
    p_list.add_argument("--name", dest="name_regex", help="regex over ANY span's name (substring match; anchor with ^ / $)")
    p_list.add_argument("--root", dest="root_regex", help="regex over the ROOT span's name, e.g. 'GET /api/users' or '^worker '")
    p_list.add_argument(
        "--exclude-root",
        dest="exclude_root_regex",
        help="drop traces whose ROOT span name matches, e.g. '^worker ' to hide periodic worker ticks",
    )
    p_list.add_argument("--kind", choices=list(_KINDS), help="filter by span kind")
    p_list.add_argument("--limit", type=int, default=20)
    p_list.add_argument("-q", "--query", dest="raw_query", help="raw TraceQL, overrides the flags above")
    p_list.set_defaults(func=cmd_list)

    p_show = sub.add_parser("show", help="Show one trace as an indented, folded span tree")
    p_show.add_argument("trace_id")
    p_show.add_argument("--full", action="store_true", help="disable sibling folding")
    p_show.add_argument("--json", action="store_true", help="dump the raw trace JSON instead")
    p_show.add_argument("--max-spans", dest="max_spans", type=int, default=200)
    p_show.set_defaults(func=cmd_show)

    p_tp = sub.add_parser("traceparent", help="Print a fresh W3C traceparent header + trace id")
    p_tp.set_defaults(func=cmd_traceparent)

    p_svc = sub.add_parser("services", help="List service names Tempo has spans for")
    p_svc.add_argument("--last", default="24h", help="time window, e.g. 1h, 168h (default 24h)")
    p_svc.set_defaults(func=cmd_services)

    p_self = sub.add_parser("selftest", help="Round-trip a synthetic trace through OTLP ingest + Tempo + rendering")
    p_self.set_defaults(func=cmd_selftest)

    return parser


def main(argv: Optional[List[str]] = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if not argv or (argv[0] not in SUBCOMMANDS and argv[0] not in ("-h", "--help")):
        argv = ["list"] + argv

    parser = build_parser()
    args = parser.parse_args(argv)
    if not hasattr(args, "func"):
        parser.print_help()
        return 0

    try:
        return args.func(args)
    except BackendUnreachable as e:
        answered = f" (HTTP {e.status})" if e.status else ""
        _err(
            f"otel backend unreachable at {e.url}{answered} — is the service enabled? "
            "(dwe services enable otel --apply)"
        )
        return EXIT_UNREACHABLE
    except QueryRejected as e:
        _err(f"tempo rejected the query (HTTP {e.status}): {e.detail}")
        return EXIT_QUERY_REJECTED
    except TraceNotFound as e:
        _err(f"trace not found: {e.trace_id}")
        return EXIT_NOT_FOUND
    except ValueError as e:
        _err(str(e))
        return EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
