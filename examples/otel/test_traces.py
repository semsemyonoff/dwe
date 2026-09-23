"""Unit tests for traces.py — pure logic only, no network.

Run with:
    python3 -m unittest discover -s workspace/otel -p 'test_*.py'
"""

import base64
import contextlib
import io
import json
import os
import re
import sys
import unittest

sys.path.insert(0, os.path.dirname(__file__))

import traces  # noqa: E402


class DurationParsingTests(unittest.TestCase):
    def test_parse_basic_units(self):
        self.assertEqual(traces.parse_duration_ns("30s"), 30_000_000_000)
        self.assertEqual(traces.parse_duration_ns("5m"), 5 * 60_000_000_000)
        self.assertEqual(traces.parse_duration_ns("2h"), 2 * 3_600_000_000_000)
        self.assertEqual(traces.parse_duration_ns("200ms"), 200_000_000)
        self.assertEqual(traces.parse_duration_ns("850us"), 850_000)
        self.assertEqual(traces.parse_duration_ns("850µs"), 850_000)
        self.assertEqual(traces.parse_duration_ns("1.5s"), 1_500_000_000)

    def test_parse_rejects_garbage(self):
        for bad in ["", "abc", "10", "10xy", "-5s"]:
            with self.assertRaises(ValueError):
                traces.parse_duration_ns(bad)

    def test_normalize_duration_token(self):
        self.assertEqual(traces.normalize_duration_token("850µs"), "850us")
        self.assertEqual(traces.normalize_duration_token(" 200ms "), "200ms")


class DurationFormattingTests(unittest.TestCase):
    def test_format_examples_from_spec(self):
        self.assertEqual(traces.format_duration(850_000), "850µs")
        self.assertEqual(traces.format_duration(12_400_000), "12.4ms")
        self.assertEqual(traces.format_duration(1_320_000_000), "1.32s")

    def test_format_none_is_question_mark(self):
        self.assertEqual(traces.format_duration(None), "?")

    def test_format_sub_microsecond(self):
        self.assertEqual(traces.format_duration(500), "500ns")

    def test_format_trims_trailing_zero(self):
        self.assertEqual(traces.format_duration(1_000_000_000), "1s")
        self.assertEqual(traces.format_duration(2_000_000), "2ms")


class TraceQLBuildingTests(unittest.TestCase):
    def test_no_flags_matches_all(self):
        self.assertEqual(traces.build_traceql(), "{}")

    def test_single_service_flag(self):
        self.assertEqual(traces.build_traceql(service="app"), '{ resource.service.name="app" }')

    def test_errors_flag(self):
        self.assertEqual(traces.build_traceql(errors=True), "{ status = error }")

    def test_min_duration(self):
        self.assertEqual(traces.build_traceql(min_duration_token="200ms"), "{ duration > 200ms }")

    def test_name_regex(self):
        # Substring semantics: TraceQL anchors fully, so the builder pads.
        self.assertEqual(traces.build_traceql(name_regex="SELECT.*"), '{ name =~ ".*SELECT.*" }')
        self.assertEqual(traces.build_traceql(name_regex="worker"), '{ name =~ ".*worker.*" }')
        self.assertEqual(traces.build_traceql(name_regex="^worker "), '{ name =~ "worker .*" }')
        self.assertEqual(traces.build_traceql(name_regex="^GET /x$"), '{ name =~ "GET /x" }')

    def test_root_filters(self):
        self.assertEqual(
            traces.build_traceql(root_regex="GET /api/users"),
            '{ trace:rootName =~ ".*GET /api/users.*" }',
        )
        self.assertEqual(
            traces.build_traceql(kind="server", exclude_root_regex="^worker "),
            '{ kind = server && trace:rootName !~ "worker .*" }',
        )

    def test_unanchor_groups_alternation(self):
        # `.*a|b.*` would be `(.*a)|(b.*)`: each branch anchored on its inner end.
        self.assertEqual(traces.unanchor("a|b"), ".*(?:a|b).*")
        self.assertEqual(traces.unanchor("^GET|POST"), "(?:GET|POST).*")
        self.assertEqual(traces.unanchor("^GET|POST$"), "GET|POST")
        self.assertEqual(
            traces.build_traceql(root_regex="GET|POST"),
            '{ trace:rootName =~ ".*(?:GET|POST).*" }',
        )
        rx = re.compile(traces.unanchor("GET|POST"))
        self.assertTrue(rx.fullmatch("handle POST /x"))
        self.assertTrue(rx.fullmatch("GET /y done"))

    def test_unanchor_keeps_explicit_wildcards_and_escaped_dollar(self):
        self.assertEqual(traces.unanchor(".*foo.*"), ".*foo.*")
        self.assertEqual(traces.unanchor("price\\$"), ".*price\\$.*")

    def test_kind(self):
        self.assertEqual(traces.build_traceql(kind="client"), "{ kind = client }")

    def test_invalid_kind_raises(self):
        with self.assertRaises(ValueError):
            traces.build_traceql(kind="bogus")

    def test_combined_predicate_order(self):
        q = traces.build_traceql(service="app", errors=True, min_duration_token="1s", name_regex="GET.*", kind="server")
        self.assertEqual(
            q,
            '{ resource.service.name="app" && status = error && duration > 1s && name =~ ".*GET.*" && kind = server }',
        )

    def test_quotes_in_service_and_regex_are_escaped(self):
        q = traces.build_traceql(service='weird"name')
        self.assertEqual(q, '{ resource.service.name="weird\\"name" }')


class IdNormalizationTests(unittest.TestCase):
    def test_pads_short_hex_trace_id(self):
        # Tempo search results sometimes drop a leading zero nibble.
        short = "f6c55be2ea14cf98c0645ff232aebe4"  # 31 chars
        self.assertEqual(len(short), 31)
        self.assertEqual(traces.normalize_id(short, 16), "0" + short)

    def test_full_length_hex_untouched(self):
        full = "289854f440c3130425eebf560a2200a3"[:32]
        self.assertEqual(traces.normalize_id(full, 16), full.lower())

    def test_base64_trace_id(self):
        raw_bytes = bytes.fromhex("0f6c55be2ea14cf98c0645ff232aebe4")
        b64 = base64.b64encode(raw_bytes).decode()
        self.assertEqual(traces.normalize_id(b64, 16), raw_bytes.hex())

    def test_base64_span_id(self):
        raw_bytes = bytes.fromhex("49c7e0b8b47abc92")
        b64 = base64.b64encode(raw_bytes).decode()
        self.assertEqual(traces.normalize_id(b64, 8), raw_bytes.hex())

    def test_empty_is_all_zero(self):
        self.assertEqual(traces.normalize_id(None, 8), "0" * 16)
        self.assertEqual(traces.normalize_id("", 16), "0" * 32)


class StatementNormalizationTests(unittest.TestCase):
    def test_collapses_whitespace(self):
        self.assertEqual(traces.normalize_statement("SELECT  *\nFROM  items"), "SELECT * FROM items")

    def test_normalizes_numeric_literal(self):
        a = traces.normalize_statement("SELECT * FROM items WHERE item_id = 100")
        b = traces.normalize_statement("SELECT * FROM items WHERE item_id = 42")
        self.assertEqual(a, b)
        self.assertEqual(a, "SELECT * FROM items WHERE item_id = ?")

    def test_normalizes_quoted_string(self):
        a = traces.normalize_statement("SELECT * FROM t WHERE name = 'alice'")
        b = traces.normalize_statement("SELECT * FROM t WHERE name = 'bob'")
        self.assertEqual(a, b)

    def test_normalizes_dollar_placeholder(self):
        a = traces.normalize_statement("SELECT * FROM t WHERE id = $1")
        b = traces.normalize_statement("SELECT * FROM t WHERE id = $2")
        self.assertEqual(a, b)

    def test_normalizes_in_list(self):
        a = traces.normalize_statement("SELECT * FROM t WHERE id IN (1, 2, 3)")
        b = traces.normalize_statement("SELECT * FROM t WHERE id IN (4, 5)")
        self.assertEqual(a, b)
        self.assertIn("IN (?)", a)

    def test_does_not_touch_identifiers_with_digits(self):
        s = traces.normalize_statement("SELECT col1 FROM table2")
        self.assertEqual(s, "SELECT col1 FROM table2")

    def test_strips_sqlc_header_comment(self):
        a = traces.normalize_statement("-- name: GetEntries :many\nSELECT * FROM entries WHERE id = 1")
        b = traces.normalize_statement("SELECT * FROM entries WHERE id = 2")
        self.assertEqual(a, b)
        self.assertNotIn("--", a)


class SqlCommentHelperTests(unittest.TestCase):
    def test_strip_sql_comments_removes_leading_line_comment(self):
        s = traces.strip_sql_comments("-- name: GetEntries :many\nSELECT * FROM entries")
        self.assertEqual(s, "SELECT * FROM entries")

    def test_strip_sql_comments_removes_leading_block_comment(self):
        s = traces.strip_sql_comments("/* generated */ SELECT 1")
        self.assertEqual(s, "SELECT 1")

    def test_strip_sql_comments_no_comment_is_unchanged(self):
        self.assertEqual(traces.strip_sql_comments("SELECT 1"), "SELECT 1")

    def test_strip_sql_comments_multiple_leading_line_comments(self):
        s = traces.strip_sql_comments("-- name: GetEntries :many\n-- another note\nSELECT 1")
        self.assertEqual(s, "SELECT 1")

    def test_sqlc_name_of_matches_header(self):
        self.assertEqual(
            traces.sqlc_name_of("-- name: GetEntries :many\nSELECT * FROM entries"), "GetEntries"
        )

    def test_sqlc_name_of_none_without_header(self):
        self.assertIsNone(traces.sqlc_name_of("SELECT * FROM entries"))

    def test_sqlc_name_of_on_bare_header_string(self):
        # The span NAME itself, not just db.statement, can be the raw header
        # line — otelpgx uses the query text's first line as the span name.
        self.assertEqual(traces.sqlc_name_of("-- name: GetEntries :many"), "GetEntries")


class DbAttributeHelperTests(unittest.TestCase):
    def test_is_db_span_old_key(self):
        self.assertTrue(traces.is_db_span({"db.system": "postgresql"}))

    def test_is_db_span_new_key(self):
        self.assertTrue(traces.is_db_span({"db.system.name": "postgresql"}))

    def test_is_db_span_false_without_either_key(self):
        self.assertFalse(traces.is_db_span({"http.method": "GET"}))

    def test_db_system_of_prefers_old_key(self):
        self.assertEqual(traces.db_system_of({"db.system": "postgresql", "db.system.name": "other"}), "postgresql")

    def test_db_system_of_falls_back_to_new_key(self):
        self.assertEqual(traces.db_system_of({"db.system.name": "postgresql"}), "postgresql")

    def test_is_db_query_span_true_with_statement(self):
        self.assertTrue(traces.is_db_query_span({"db.system": "postgresql", "db.statement": "SELECT 1"}))

    def test_is_db_query_span_true_with_operation_only(self):
        self.assertTrue(traces.is_db_query_span({"db.system.name": "redis", "db.operation.name": "GET"}))

    def test_is_db_query_span_false_for_connection_lifecycle_span(self):
        # pgx's `pool.acquire`: tagged db.system(.name) but no statement or
        # operation of its own — not a query.
        self.assertFalse(
            traces.is_db_query_span({"db.system.name": "postgresql", "server.address": "postgres"})
        )

    def test_is_db_query_span_false_without_db_system(self):
        self.assertFalse(traces.is_db_query_span({"http.method": "GET"}))

    def test_db_operation_of_old_key(self):
        self.assertEqual(traces.db_operation_of({"db.operation": "SELECT"}), "SELECT")

    def test_db_operation_of_new_key(self):
        self.assertEqual(traces.db_operation_of({"db.operation.name": "SELECT"}), "SELECT")

    def test_db_operation_of_none(self):
        self.assertIsNone(traces.db_operation_of({}))


class FormatStatementSummaryTests(unittest.TestCase):
    def test_plain_statement_no_prefix(self):
        self.assertEqual(traces.format_statement_summary("SELECT * FROM entries"), "SELECT * FROM entries")

    def test_sqlc_header_becomes_name_prefix(self):
        text = traces.format_statement_summary("-- name: GetEntries :many\nSELECT * FROM entries WHERE id = 1")
        self.assertEqual(text, "GetEntries: SELECT * FROM entries WHERE id = 1")

    def test_normalize_true_folds_literals_and_keeps_prefix(self):
        text = traces.format_statement_summary(
            "-- name: GetEntries :many\nSELECT * FROM entries WHERE id = 1", normalize=True
        )
        self.assertEqual(text, "GetEntries: SELECT * FROM entries WHERE id = ?")


class DisplaySpanNameTests(unittest.TestCase):
    def _span_obj(self, name, attributes=None):
        return traces.Span(
            trace_id="0f6c55be2ea14cf98c0645ff232aebe4",
            span_id="aaaa000000000001",
            parent_id=None,
            name=name,
            kind="client",
            start_ns=1_000_000_000,
            end_ns=1_001_000_000,
            service="app",
            attributes=attributes or {},
            status_code="unset",
            status_message="",
        )

    def test_normal_name_is_unchanged(self):
        span = self._span_obj("SELECT")
        self.assertEqual(traces.display_span_name(span), "SELECT")

    def test_comment_name_falls_back_to_sqlc_name_from_span_name(self):
        span = self._span_obj("-- name: GetEntries :many")
        self.assertEqual(traces.display_span_name(span), "GetEntries")

    def test_comment_name_falls_back_to_sqlc_name_from_statement(self):
        span = self._span_obj(
            "--",
            attributes={"db.query.text": "-- name: GetEntries :many\nSELECT * FROM entries"},
        )
        self.assertEqual(traces.display_span_name(span), "GetEntries")

    def test_bare_dash_dash_without_sqlc_name_falls_back_to_statement_snippet(self):
        span = self._span_obj("--", attributes={"db.query.text": "SELECT * FROM entries"})
        self.assertEqual(traces.display_span_name(span), "SELECT * FROM entries")

    def test_bare_dash_dash_without_any_statement_stays_literal(self):
        span = self._span_obj("--")
        self.assertEqual(traces.display_span_name(span), "--")


def _resource_span(service, spans):
    return {"resourceSpans": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": service}}]}, "scopeSpans": [{"scope": {"name": "test"}, "spans": spans}]}]}


def _span(span_id, parent_id, name, kind="SPAN_KIND_INTERNAL", start=1_000_000_000, dur=1_000_000, attrs=None, status=None, events=None):
    d = {
        "traceId": "0f6c55be2ea14cf98c0645ff232aebe4",
        "spanId": span_id,
        "name": name,
        "kind": kind,
        "startTimeUnixNano": str(start),
        "endTimeUnixNano": str(start + dur),
        "attributes": attrs or [],
    }
    if parent_id:
        d["parentSpanId"] = parent_id
    if status:
        d["status"] = status
    if events:
        d["events"] = events
    return d


def _attr(k, v, t="stringValue"):
    return {"key": k, "value": {t: v}}


class TreeBuildingTests(unittest.TestCase):
    def test_single_root(self):
        trace = _resource_span("app", [_span("aaaa000000000001", None, "root")])
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        self.assertEqual(len(roots), 1)
        self.assertEqual(roots[0].span.name, "root")
        self.assertFalse(roots[0].orphan)

    def test_parent_child(self):
        trace = _resource_span(
            "app",
            [
                _span("aaaa000000000001", None, "root", start=1_000_000_000, dur=10_000_000),
                _span("aaaa000000000002", "aaaa000000000001", "child", start=1_001_000_000, dur=1_000_000),
            ],
        )
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        self.assertEqual(len(roots), 1)
        self.assertEqual(len(roots[0].children), 1)
        self.assertEqual(roots[0].children[0].span.name, "child")

    def test_orphan_becomes_root(self):
        trace = _resource_span(
            "app",
            [_span("aaaa000000000002", "deadbeef00000001", "orphan-child")],
        )
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        self.assertEqual(len(roots), 1)
        self.assertTrue(roots[0].orphan)

    def test_orphan_root_renders_as_remote_parent(self):
        trace = _resource_span(
            "app",
            [_span("aaaa000000000002", "deadbeef00000001", "orphan-child")],
        )
        spans = traces.flatten_trace(trace)
        text = traces.render_show(spans[0].trace_id, spans)
        self.assertIn("(remote parent)", text)
        self.assertNotIn("(orphan)", text)

    def test_children_sorted_by_start_time(self):
        trace = _resource_span(
            "app",
            [
                _span("aaaa000000000001", None, "root", start=1_000_000_000, dur=10_000_000),
                _span("aaaa000000000003", "aaaa000000000001", "second", start=1_005_000_000, dur=1_000_000),
                _span("aaaa000000000002", "aaaa000000000001", "first", start=1_001_000_000, dur=1_000_000),
            ],
        )
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        names = [c.span.name for c in roots[0].children]
        self.assertEqual(names, ["first", "second"])

    def test_self_time_subtracts_direct_children(self):
        trace = _resource_span(
            "app",
            [
                _span("aaaa000000000001", None, "root", start=1_000_000_000, dur=10_000_000),
                _span("aaaa000000000002", "aaaa000000000001", "child", start=1_001_000_000, dur=3_000_000),
            ],
        )
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        self.assertEqual(traces.self_time_ns(roots[0]), 10_000_000 - 3_000_000)


class FoldingTests(unittest.TestCase):
    def _n1_trace(self, n=12):
        root = _span("aaaa000000000001", None, "root", kind="SPAN_KIND_SERVER", start=1_000_000_000, dur=50_000_000)
        children = [
            _span(
                f"bbbb0000000000{i:02x}",
                "aaaa000000000001",
                "SELECT",
                kind="SPAN_KIND_CLIENT",
                start=1_001_000_000 + i * 1_000_000,
                dur=1_000_000,
                attrs=[_attr("db.system", "postgresql"), _attr("db.statement", f"SELECT * FROM items WHERE item_id = {100+i}")],
            )
            for i in range(n)
        ]
        return _resource_span("app", [root] + children)

    def test_folds_matching_siblings(self):
        trace = self._n1_trace(12)
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        units = traces.flatten_render_units(roots, roots[0].span.start_ns, full=False)
        fold_lines = [u for u in units if u.span_count > 1]
        self.assertEqual(len(fold_lines), 1)
        self.assertEqual(fold_lines[0].span_count, 12)
        text = "\n".join(fold_lines[0].lines)
        self.assertIn("×12", text)
        self.assertIn("possible N+1", text)

    def test_below_five_no_n1_marker(self):
        trace = self._n1_trace(3)
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        units = traces.flatten_render_units(roots, roots[0].span.start_ns, full=False)
        fold_lines = [u for u in units if u.span_count > 1]
        self.assertEqual(len(fold_lines), 1)
        text = "\n".join(fold_lines[0].lines)
        self.assertIn("×3", text)
        self.assertNotIn("possible N+1", text)

    def test_full_disables_folding(self):
        trace = self._n1_trace(12)
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        units = traces.flatten_render_units(roots, roots[0].span.start_ns, full=True)
        # root + 12 individual children, no fold unit
        self.assertEqual(len(units), 13)
        self.assertTrue(all(u.span_count == 1 for u in units))

    def _pool_acquire_trace(self, n=12):
        # pgx's pool.acquire: db.system(.name) present, but no statement or
        # operation — a connection-lifecycle span, not a query.
        root = _span("aaaa000000000001", None, "root", kind="SPAN_KIND_SERVER", start=1_000_000_000, dur=50_000_000)
        children = [
            _span(
                f"cccc0000000000{i:02x}",
                "aaaa000000000001",
                "pool.acquire",
                kind="SPAN_KIND_CLIENT",
                start=1_001_000_000 + i * 1_000_000,
                dur=1_000,
                attrs=[_attr("db.system.name", "postgresql"), _attr("server.address", "postgres")],
            )
            for i in range(n)
        ]
        return _resource_span("app", [root] + children)

    def test_folded_non_db_query_span_has_no_n1_marker(self):
        trace = self._pool_acquire_trace(12)
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        units = traces.flatten_render_units(roots, roots[0].span.start_ns, full=False)
        fold_lines = [u for u in units if u.span_count > 1]
        self.assertEqual(len(fold_lines), 1)
        self.assertEqual(fold_lines[0].span_count, 12)
        text = "\n".join(fold_lines[0].lines)
        self.assertIn("×12", text)
        self.assertIn("pool.acquire", text)
        self.assertNotIn("possible N+1", text)

    def test_different_statements_do_not_fold(self):
        root = _span("aaaa000000000001", None, "root", kind="SPAN_KIND_SERVER", start=1_000_000_000, dur=50_000_000)
        c1 = _span(
            "bbbb000000000001", "aaaa000000000001", "SELECT", kind="SPAN_KIND_CLIENT",
            start=1_001_000_000, dur=1_000_000,
            attrs=[_attr("db.system", "postgresql"), _attr("db.statement", "SELECT * FROM items")],
        )
        c2 = _span(
            "bbbb000000000002", "aaaa000000000001", "SELECT", kind="SPAN_KIND_CLIENT",
            start=1_002_000_000, dur=1_000_000,
            attrs=[_attr("db.system", "postgresql"), _attr("db.statement", "SELECT * FROM users")],
        )
        trace = _resource_span("app", [root, c1, c2])
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        units = traces.flatten_render_units(roots, roots[0].span.start_ns, full=False)
        self.assertEqual(len(units), 3)  # root + 2 separate (not folded)


class GoStyleSqlcTraceRenderingTests(unittest.TestCase):
    """End-to-end: a Go/otelpgx-style trace using the newer semconv >= 1.40
    keys (db.system.name, db.query.text, db.operation.name) and a
    sqlc-generated span name (`-- name: GetEntries :many`)."""

    def _trace(self, n=25):
        root = _span(
            "aaaa000000000001", None, "GET /entries", kind="SPAN_KIND_SERVER", start=1_000_000_000, dur=80_000_000
        )
        children = [
            _span(
                f"bbbb0000000000{i:02x}",
                "aaaa000000000001",
                "-- name: GetEntries :many",
                kind="SPAN_KIND_CLIENT",
                start=1_001_000_000 + i * 1_000_000,
                dur=1_000_000,
                attrs=[
                    _attr("db.system.name", "postgresql"),
                    _attr("db.query.text", f"-- name: GetEntries :many\nSELECT * FROM entries WHERE id = {100 + i}"),
                    _attr("db.operation.name", "SELECT"),
                ],
            )
            for i in range(n)
        ]
        return _resource_span("app", [root] + children)

    def test_header_counts_db_spans(self):
        trace = self._trace(25)
        spans = traces.flatten_trace(trace)
        text = traces.render_show("0f6c55be2ea14cf98c0645ff232aebe4", spans)
        self.assertIn("db: 25 spans", text)

    def test_fold_marks_possible_n_plus_1_and_renders_sqlc_name(self):
        trace = self._trace(25)
        spans = traces.flatten_trace(trace)
        roots = traces.build_tree(spans)
        units = traces.flatten_render_units(roots, roots[0].span.start_ns, full=False)
        fold_lines = [u for u in units if u.span_count > 1]
        self.assertEqual(len(fold_lines), 1)
        self.assertEqual(fold_lines[0].span_count, 25)
        text = "\n".join(fold_lines[0].lines)
        self.assertIn("×25", text)
        self.assertIn("possible N+1", text)
        self.assertIn("GetEntries", text)
        self.assertNotIn("-- name:", text)

    def test_show_renders_sqlc_name_not_raw_comment(self):
        trace = self._trace(25)
        spans = traces.flatten_trace(trace)
        text = traces.render_show("0f6c55be2ea14cf98c0645ff232aebe4", spans)
        self.assertIn("GetEntries", text)
        self.assertNotIn("-- name:", text)
        self.assertNotIn("-- [client]", text)


class ErrorRenderingTests(unittest.TestCase):
    def test_error_status_and_exception_rendered(self):
        root = _span(
            "aaaa000000000001", None, "op", kind="SPAN_KIND_INTERNAL", start=1_000_000_000, dur=1_000_000,
            status={"code": "STATUS_CODE_ERROR", "message": "boom"},
            events=[{
                "timeUnixNano": "1000500000",
                "name": "exception",
                "attributes": [
                    _attr("exception.type", "ValueError"),
                    _attr("exception.message", "bad thing"),
                    _attr("exception.stacktrace", 'File "/workspace/src/app/x.py", line 1\nValueError: bad thing'),
                ],
            }],
        )
        trace = _resource_span("app", [root])
        spans = traces.flatten_trace(trace)
        text = traces.render_show("0f6c55be2ea14cf98c0645ff232aebe4", spans)
        self.assertIn("ERROR boom", text)
        self.assertIn("ValueError: bad thing", text)
        self.assertIn("/workspace/src/app/x.py", text)

    def test_stacktrace_prefers_app_lines(self):
        lines = "\n".join([f"lib line {i}" for i in range(10)] + ["at /workspace/src/app/y.py line 5"])
        picked = traces.pick_stacktrace_lines(lines)
        self.assertEqual(picked, ["at /workspace/src/app/y.py line 5"])

    def test_stacktrace_falls_back_to_last_lines(self):
        lines = "\n".join(f"lib line {i}" for i in range(10))
        picked = traces.pick_stacktrace_lines(lines)
        self.assertEqual(len(picked), 5)
        self.assertEqual(picked, [f"lib line {i}" for i in range(5, 10)])


class ListLineTests(unittest.TestCase):
    def test_summarize_line_contains_expected_fields(self):
        trace = {
            "traceID": "f6c55be2ea14cf98c0645ff232aebe4",
            "rootServiceName": "app",
            "rootTraceName": "GET /api/openapi.json",
            "startTimeUnixNano": "1789981733233437672",
            "durationMs": 2,
            "spanSet": {"matched": 3},
            "serviceStats": {"app": {"spanCount": 3}},
        }
        line = traces.summarize_trace_line(trace)
        self.assertIn("0f6c55be2ea14cf98c0645ff232aebe4", line)
        self.assertIn("app", line)
        self.assertIn("GET /api/openapi.json", line)
        self.assertIn("matched=3", line)
        self.assertNotIn("ERR", line)

    def test_error_marker_from_service_stats(self):
        trace = {
            "traceID": "abc",
            "serviceStats": {"app": {"spanCount": 2, "errorCount": 1}},
        }
        self.assertTrue(traces.trace_has_error(trace))
        line = traces.summarize_trace_line(trace)
        self.assertTrue(line.endswith("ERR"))

    def test_duration_fallback_from_spanset(self):
        trace = {
            "startTimeUnixNano": "1000000000",
            "spanSet": {"spans": [{"startTimeUnixNano": "1000000000", "durationNanos": "5000000"}]},
        }
        self.assertEqual(traces.trace_duration_ns(trace), 5_000_000)


def _search_trace(trace_id, root_name, duration_ms=None, matched=None):
    t = {"traceID": trace_id, "rootServiceName": "app", "rootTraceName": root_name}
    if duration_ms is not None:
        t["durationMs"] = duration_ms
    if matched is not None:
        t["spanSet"] = {"matched": matched}
    return t


class SummaryQueryBuildingTests(unittest.TestCase):
    def test_no_flags(self):
        q_all, q_db, q_err = traces.build_summary_queries()
        self.assertEqual(q_all, "{}")
        self.assertEqual(
            q_db,
            "{ (span.db.system != nil || span.db.system.name != nil)"
            " && (span.db.statement != nil || span.db.query.text != nil"
            " || span.db.operation != nil || span.db.operation.name != nil) }",
        )
        self.assertEqual(q_err, "{ status = error }")

    def test_service_and_root_filters_carry_into_all_three(self):
        q_all, q_db, q_err = traces.build_summary_queries(
            service="app", root_regex="^worker ", exclude_root_regex="PING"
        )
        expected_prefix = '{ resource.service.name="app" && trace:rootName =~ "worker .*" && trace:rootName !~ ".*PING.*"'
        self.assertTrue(q_all.startswith(expected_prefix))
        self.assertIn(
            "(span.db.system != nil || span.db.system.name != nil)"
            " && (span.db.statement != nil || span.db.query.text != nil"
            " || span.db.operation != nil || span.db.operation.name != nil)",
            q_db,
        )
        self.assertIn("status = error", q_err)
        self.assertTrue(q_db.startswith(expected_prefix))
        self.assertTrue(q_err.startswith(expected_prefix))


class TruncationDetectionTests(unittest.TestCase):
    def test_detects_queries_at_limit(self):
        named = [("all", [1, 2, 3]), ("db", [1]), ("errors", [1, 2, 3])]
        self.assertEqual(traces.detect_truncated_queries(3, named), ["all", "errors"])

    def test_none_truncated(self):
        named = [("all", [1, 2]), ("db", []), ("errors", [1])]
        self.assertEqual(traces.detect_truncated_queries(3, named), [])


class SummaryAggregationTests(unittest.TestCase):
    def test_join_aggregates_by_root(self):
        all_traces = [
            _search_trace("1111111111111111", "GET /api/things", duration_ms=30),
            _search_trace("2222222222222222", "GET /api/things", duration_ms=40),
            _search_trace("3333333333333333", "worker dispatch", duration_ms=5),
        ]
        db_traces = [
            _search_trace("1111111111111111", "GET /api/things", matched=30),
            _search_trace("2222222222222222", "GET /api/things", matched=32),
        ]
        err_traces = [_search_trace("2222222222222222", "GET /api/things")]
        rows = traces.build_summary_rows(all_traces, db_traces, err_traces)
        by_root = {r.root: r for r in rows}
        things = by_root["GET /api/things"]
        self.assertEqual(things.trace_count, 2)
        self.assertEqual(things.db_total, 62)
        self.assertEqual(things.db_max, 32)
        self.assertEqual(things.db_avg, 31.0)
        self.assertEqual(things.error_count, 1)
        worker = by_root["worker dispatch"]
        self.assertEqual(worker.trace_count, 1)
        self.assertEqual(worker.db_total, 0)
        self.assertEqual(worker.error_count, 0)

    def test_root_with_zero_db_spans_still_appears(self):
        all_traces = [_search_trace("1111111111111111", "worker dispatch", duration_ms=5)]
        rows = traces.build_summary_rows(all_traces, [], [])
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0].db_total, 0)
        self.assertEqual(rows[0].db_avg, 0.0)

    def test_trace_seen_only_by_db_query_is_not_dropped(self):
        # Defensive: query B is supposed to be exhaustive, but a trace absent
        # from it must still be counted rather than silently disappearing.
        db_traces = [_search_trace("9999999999999999", "GET /api/orphan", matched=5)]
        rows = traces.build_summary_rows([], db_traces, [])
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0].root, "GET /api/orphan")
        self.assertEqual(rows[0].db_total, 5)


class SummarySortingTests(unittest.TestCase):
    def _rows(self):
        return [
            traces.SummaryRow(root="a", trace_count=10, db_total=100, db_max=15, dur_total_ns=100, error_count=0),
            traces.SummaryRow(root="b", trace_count=5, db_total=200, db_max=50, dur_total_ns=500, error_count=3),
            traces.SummaryRow(root="c", trace_count=20, db_total=50, db_max=5, dur_total_ns=50, error_count=1),
        ]

    def test_sort_by_db_total_default(self):
        rows = traces.sort_summary_rows(self._rows(), "db")
        self.assertEqual([r.root for r in rows], ["b", "a", "c"])

    def test_sort_by_dbmax(self):
        rows = traces.sort_summary_rows(self._rows(), "dbmax")
        self.assertEqual([r.root for r in rows], ["b", "a", "c"])

    def test_sort_by_count(self):
        rows = traces.sort_summary_rows(self._rows(), "count")
        self.assertEqual([r.root for r in rows], ["c", "a", "b"])

    def test_sort_by_errors(self):
        rows = traces.sort_summary_rows(self._rows(), "errors")
        self.assertEqual([r.root for r in rows], ["b", "c", "a"])

    def test_invalid_sort_raises(self):
        with self.assertRaises(ValueError):
            traces.sort_summary_rows(self._rows(), "bogus")


class SummaryTableRenderingTests(unittest.TestCase):
    def test_table_has_header_and_aligned_rows(self):
        rows = [
            traces.SummaryRow(root="GET /api/things", trace_count=14, db_total=434, db_max=32, dur_total_ns=14 * 33_100_000, dur_max_ns=51_000_000, error_count=0),
            traces.SummaryRow(root="worker dispatch", trace_count=90, db_total=180, db_max=2, dur_total_ns=90 * 6_200_000, dur_max_ns=9_800_000, error_count=0),
        ]
        lines = traces.render_summary_table(rows)
        self.assertEqual(len(lines), 3)
        header = lines[0]
        for col in ("root", "traces", "db/trace avg", "max", "db total", "dur avg", "errors"):
            self.assertIn(col, header)
        self.assertIn("GET /api/things", lines[1])
        self.assertIn("31.0", lines[1])  # db/trace avg = 434/14
        self.assertIn("434", lines[1])

    def test_root_names_truncate_at_48_chars(self):
        long_root = "GET /api/" + "x" * 60
        rows = [traces.SummaryRow(root=long_root, trace_count=1, db_total=1, db_max=1, dur_total_ns=1)]
        lines = traces.render_summary_table(rows)
        first_cell = lines[1].split("  ")[0]
        self.assertLessEqual(len(first_cell), 48)
        self.assertTrue(first_cell.endswith("…"))


class SummaryHintTests(unittest.TestCase):
    def test_hint_on_high_db_avg(self):
        rows = [
            traces.SummaryRow(root="GET /api/things", trace_count=14, db_total=434, db_max=32),
            traces.SummaryRow(root="worker dispatch", trace_count=90, db_total=180, db_max=2),
        ]
        hint = traces.summary_hint_line(rows)
        self.assertIsNotNone(hint)
        self.assertIn("GET /api/things", hint)
        self.assertIn("31.0", hint)
        self.assertIn("--root", hint)

    def test_no_hint_below_threshold(self):
        rows = [traces.SummaryRow(root="worker dispatch", trace_count=90, db_total=180, db_max=2)]
        self.assertIsNone(traces.summary_hint_line(rows))

    def test_hint_names_up_to_three_roots_and_skips_the_unnamed_row(self):
        rows = [
            traces.SummaryRow(root="-", trace_count=2, db_total=200, db_max=100),
            traces.SummaryRow(root="worker sync", trace_count=3, db_total=246, db_max=82),
            traces.SummaryRow(root="GET /api/things", trace_count=14, db_total=434, db_max=32),
            traces.SummaryRow(root="GET /a", trace_count=1, db_total=20, db_max=20),
            traces.SummaryRow(root="GET /b", trace_count=1, db_total=16, db_max=16),
        ]
        hint = traces.summary_hint_line(rows)
        lines = hint.splitlines()
        self.assertEqual(len(lines), 3)
        self.assertIn("worker sync", lines[0])
        self.assertIn("GET /api/things", lines[1])
        self.assertNotIn('"-"', hint)

    def test_hint_escapes_regex_metacharacters_in_root(self):
        rows = [traces.SummaryRow(root="GET /api/things/{id}", trace_count=1, db_total=20, db_max=20)]
        hint = traces.summary_hint_line(rows)
        self.assertIn(re.escape("GET /api/things/{id}"), hint)


class TraceparentTests(unittest.TestCase):
    def test_shape(self):
        header, trace_id = traces.make_traceparent()
        self.assertRegex(header, r"^00-[0-9a-f]{32}-[0-9a-f]{16}-01$")
        self.assertIn(trace_id, header)
        self.assertEqual(len(trace_id), 32)


class TagValuesTests(unittest.TestCase):
    """`services` must ask for a time range: without start/end Tempo answers
    from its live store only, so a service whose spans were already flushed
    to blocks vanished from the list while a fresh selftest trace showed up."""

    def setUp(self):
        self.calls = []
        self._orig = traces._http_get

    def tearDown(self):
        traces._http_get = self._orig

    def _stub(self, status, payload):
        def fake(url, params=None, timeout=8.0):
            self.calls.append((url, params))
            return status, json.dumps(payload).encode("utf-8")

        traces._http_get = fake

    def test_uses_scoped_v2_endpoint_with_time_range(self):
        self._stub(200, {"tagValues": []})
        traces.tempo_tag_values("http://tempo:3200", "resource.service.name", 100, 200)
        url, params = self.calls[0]
        self.assertEqual(url, "http://tempo:3200/api/v2/search/tag/resource.service.name/values")
        self.assertEqual(params, {"start": 100, "end": 200})

    def test_parses_v2_typed_values_and_dedupes(self):
        self._stub(
            200,
            {
                "tagValues": [
                    {"type": "string", "value": "traces-selftest"},
                    {"type": "string", "value": "main"},
                    {"type": "string", "value": "main"},
                    {"type": "string", "value": ""},
                ]
            },
        )
        got = traces.tempo_tag_values("http://tempo:3200", "resource.service.name", 0, 1)
        self.assertEqual(got, ["traces-selftest", "main"])

    def test_accepts_v1_plain_string_values(self):
        self._stub(200, {"tagValues": ["main"]})
        self.assertEqual(traces.tempo_tag_values("http://t", "resource.service.name", 0, 1), ["main"])

    def test_non_200_is_backend_unreachable(self):
        self._stub(500, {})
        with self.assertRaises(traces.BackendUnreachable):
            traces.tempo_tag_values("http://t", "resource.service.name", 0, 1)

    def test_services_subcommand_passes_last_window(self):
        self._stub(200, {"tagValues": [{"type": "string", "value": "main"}]})
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(traces.main(["services", "--last", "2h"]), 0)
        self.assertEqual(out.getvalue(), "main\n")
        _, params = self.calls[0]
        self.assertEqual(params["end"] - params["start"], 7200)


class TempoErrorMappingTests(unittest.TestCase):
    """A 4xx means Tempo is up and refused the request, so it must not be
    reported as "backend unreachable … is the service enabled?"."""

    def setUp(self):
        self._orig = traces._http_get

    def tearDown(self):
        traces._http_get = self._orig

    def _stub(self, status, body):
        def fake(url, params=None, timeout=8.0):
            return status, body

        traces._http_get = fake

    def _run_main(self, argv):
        err = io.StringIO()
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(err):
            code = traces.main(argv)
        return code, err.getvalue()

    def test_400_on_search_is_query_rejected_with_tempo_reason(self):
        self._stub(400, b'invalid TraceQL query: parse error at line 1, col 3: syntax error\n')
        with self.assertRaises(traces.QueryRejected) as ctx:
            traces.tempo_search("http://t", "{ bogus")
        self.assertEqual(ctx.exception.status, 400)
        self.assertEqual(ctx.exception.detail, "invalid TraceQL query: parse error at line 1, col 3: syntax error")

    def test_4xx_exits_3_and_prints_reason(self):
        self._stub(400, b"range specified by start and end exceeds 168h0m0s. received start=1 end=2")
        code, err = self._run_main(["list", "--last", "400h"])
        self.assertEqual(code, traces.EXIT_QUERY_REJECTED)
        self.assertEqual(code, 3)
        self.assertIn("tempo rejected the query (HTTP 400): range specified by start and end exceeds 168h0m0s", err)
        self.assertNotIn("unreachable", err)

    def test_4xx_on_tag_values_and_trace_fetch(self):
        self._stub(400, b"bad request")
        with self.assertRaises(traces.QueryRejected):
            traces.tempo_tag_values("http://t", "resource.service.name", 0, 1)
        with self.assertRaises(traces.QueryRejected):
            traces.tempo_get_trace("http://t", "0" * 32)

    def test_404_on_trace_fetch_stays_not_found(self):
        self._stub(404, b"trace not found")
        with self.assertRaises(traces.TraceNotFound):
            traces.tempo_get_trace("http://t", "0" * 32)

    def test_5xx_is_unreachable_with_status(self):
        for status in (500, 502, 503):
            self._stub(status, b"Tempo is starting")
            code, err = self._run_main(["list"])
            self.assertEqual(code, traces.EXIT_UNREACHABLE)
            self.assertIn(f"otel backend unreachable at http://otel:3200 (HTTP {status})", err)

    def test_connection_error_is_unreachable_without_status(self):
        def fake(url, params=None, timeout=8.0):
            raise traces.BackendUnreachable(url)

        traces._http_get = fake
        code, err = self._run_main(["list"])
        self.assertEqual(code, 2)
        self.assertIn("otel backend unreachable at http://otel:3200/api/search — is the service enabled?", err)

    def test_long_body_is_trimmed_to_one_line(self):
        detail = traces._error_detail(("x" * 1000 + "\n\tmore").encode())
        self.assertLessEqual(len(detail), traces._DETAIL_MAX)
        self.assertTrue(detail.endswith("…"))
        self.assertNotIn("\n", detail)
        self.assertEqual(traces._error_detail(b""), "(empty response body)")

    def test_help_documents_exit_codes(self):
        self.assertIn("3  Tempo rejected the query", traces.build_parser().format_help())


class SelftestFixtureTests(unittest.TestCase):
    """Reuse the selftest synthetic-trace builder as a fixture, converted
    into Tempo's own response shape (`{"trace": {"resourceSpans": [...]}}`),
    to make sure the full render pipeline sees the N+1 fold and the error."""

    def test_selftest_payload_renders_expected_markers(self):
        payload, trace_id = traces.build_selftest_payload()
        trace_data = {"resourceSpans": payload["resourceSpans"]}
        spans = traces.flatten_trace(trace_data)
        text = traces.render_show(trace_id, spans, full=False, max_spans=200)
        self.assertIn("×12", text)
        self.assertIn("possible N+1", text)
        self.assertIn("ValueError: unexpected null field", text)
        self.assertIn("/workspace/src/app/api/items.py", text)


if __name__ == "__main__":
    unittest.main()
