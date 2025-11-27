"""
Tests for Prometheus validation and sanitization functions.

These tests ensure that PromQL injection vulnerabilities are prevented
by validating all user inputs before they are used in queries.
"""

import pytest
from app.utils.prometheus_validation import (
    sanitize_label_value,
    sanitize_metric_name,
    sanitize_label_name,
    build_label_matcher,
    build_label_filter,
    validate_step,
    PromQLValidationError
)


class TestSanitizeLabelValue:
    """Test label value sanitization."""

    def test_valid_alphanumeric(self):
        """Test valid alphanumeric values."""
        assert sanitize_label_value("node01") == "node01"
        assert sanitize_label_value("server123") == "server123"

    def test_valid_with_hyphens(self):
        """Test valid values with hyphens."""
        assert sanitize_label_value("node-01") == "node-01"
        assert sanitize_label_value("my-server-name") == "my-server-name"

    def test_valid_with_underscores(self):
        """Test valid values with underscores."""
        assert sanitize_label_value("node_01") == "node_01"
        assert sanitize_label_value("my_server") == "my_server"

    def test_valid_with_periods(self):
        """Test valid values with periods (domain names)."""
        assert sanitize_label_value("server.example.com") == "server.example.com"
        assert sanitize_label_value("node.local") == "node.local"

    def test_valid_with_colons(self):
        """Test valid values with colons (IP:port)."""
        assert sanitize_label_value("192.168.1.1:9102") == "192.168.1.1:9102"
        assert sanitize_label_value("10.0.0.1:8080") == "10.0.0.1:8080"

    def test_valid_with_slashes(self):
        """Test valid values with forward slashes (paths)."""
        assert sanitize_label_value("path/to/file") == "path/to/file"
        assert sanitize_label_value("/var/lib/data") == "/var/lib/data"

    def test_valid_uuid(self):
        """Test valid UUID values."""
        uuid = "550e8400-e29b-41d4-a716-446655440000"
        assert sanitize_label_value(uuid) == uuid

    def test_invalid_with_quotes(self):
        """Test that quotes are rejected (injection risk)."""
        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value('node"injection')

        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node'injection")

    def test_invalid_with_braces(self):
        """Test that braces are rejected (injection risk)."""
        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node{attack}")

        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node}attack")

    def test_invalid_with_parentheses(self):
        """Test that parentheses are rejected (injection risk)."""
        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node(attack)")

        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node)attack")

    def test_invalid_with_semicolon(self):
        """Test that semicolons are rejected."""
        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node;drop table")

    def test_invalid_with_whitespace(self):
        """Test that whitespace is rejected."""
        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node name with spaces")

    def test_invalid_with_backslash(self):
        """Test that backslashes are rejected."""
        with pytest.raises(PromQLValidationError, match="invalid characters"):
            sanitize_label_value("node\\escape")

    def test_invalid_empty_string(self):
        """Test that empty strings are rejected."""
        with pytest.raises(PromQLValidationError, match="cannot be empty"):
            sanitize_label_value("")

    def test_invalid_too_long(self):
        """Test that values exceeding max length are rejected."""
        long_value = "a" * 256
        with pytest.raises(PromQLValidationError, match="exceeds maximum length"):
            sanitize_label_value(long_value)

    def test_injection_attempt_closing_brace(self):
        """Test blocking injection attempt with closing brace."""
        with pytest.raises(PromQLValidationError):
            sanitize_label_value('node"}}or{__name__=~".*"}')

    def test_injection_attempt_union(self):
        """Test blocking SQL-like injection attempts."""
        with pytest.raises(PromQLValidationError):
            sanitize_label_value('node" or "1"="1')


class TestSanitizeMetricName:
    """Test metric name sanitization."""

    def test_valid_metric_name(self):
        """Test valid metric names."""
        assert sanitize_metric_name("kepler_node_power") == "kepler_node_power"
        assert sanitize_metric_name("cpu_usage_total") == "cpu_usage_total"

    def test_valid_with_colons(self):
        """Test metric names with colons (recording rules)."""
        assert sanitize_metric_name("cpu:usage:rate5m") == "cpu:usage:rate5m"

    def test_invalid_starting_with_digit(self):
        """Test that metric names starting with digits are rejected."""
        with pytest.raises(PromQLValidationError, match="Invalid metric name"):
            sanitize_metric_name("123invalid")

    def test_invalid_with_hyphens(self):
        """Test that hyphens are rejected in metric names."""
        with pytest.raises(PromQLValidationError, match="Invalid metric name"):
            sanitize_metric_name("metric-name")

    def test_invalid_empty(self):
        """Test that empty metric names are rejected."""
        with pytest.raises(PromQLValidationError, match="cannot be empty"):
            sanitize_metric_name("")


class TestSanitizeLabelName:
    """Test label name sanitization."""

    def test_valid_label_name(self):
        """Test valid label names."""
        assert sanitize_label_name("exported_instance") == "exported_instance"
        assert sanitize_label_name("node_name") == "node_name"

    def test_invalid_starting_with_digit(self):
        """Test that label names starting with digits are rejected."""
        with pytest.raises(PromQLValidationError, match="Invalid label name"):
            sanitize_label_name("123invalid")

    def test_invalid_reserved_prefix(self):
        """Test that labels starting with __ are rejected."""
        with pytest.raises(PromQLValidationError, match="reserved for internal use"):
            sanitize_label_name("__reserved")

    def test_invalid_empty(self):
        """Test that empty label names are rejected."""
        with pytest.raises(PromQLValidationError, match="cannot be empty"):
            sanitize_label_name("")


class TestBuildLabelMatcher:
    """Test label matcher building."""

    def test_equal_matcher(self):
        """Test building an equality matcher."""
        result = build_label_matcher("node", "node-01")
        assert result == 'node="node-01"'

    def test_not_equal_matcher(self):
        """Test building a not-equal matcher."""
        result = build_label_matcher("node", "node-01", "!=")
        assert result == 'node!="node-01"'

    def test_regex_matcher(self):
        """Test building a regex matcher."""
        result = build_label_matcher("instance", "192.168.1.1:9102", "=~")
        assert result == 'instance=~"192.168.1.1:9102"'

    def test_invalid_operator(self):
        """Test that invalid operators are rejected."""
        with pytest.raises(PromQLValidationError, match="Invalid operator"):
            build_label_matcher("node", "node-01", "INVALID")

    def test_invalid_label_value(self):
        """Test that invalid label values are rejected."""
        with pytest.raises(PromQLValidationError):
            build_label_matcher("node", "node{injection}")


class TestBuildLabelFilter:
    """Test label filter building."""

    def test_single_filter(self):
        """Test building a filter with one label."""
        result = build_label_filter({"node": "node-01"})
        assert result == '{node="node-01"}'

    def test_multiple_filters(self):
        """Test building a filter with multiple labels."""
        result = build_label_filter({
            "node": "node-01",
            "cluster": "prod"
        })
        # Order might vary, so check both possibilities
        assert result in [
            '{node="node-01",cluster="prod"}',
            '{cluster="prod",node="node-01"}'
        ]

    def test_empty_filter(self):
        """Test that empty filter dict returns empty string."""
        result = build_label_filter({})
        assert result == ""

    def test_none_values_skipped(self):
        """Test that None values are skipped."""
        result = build_label_filter({
            "node": "node-01",
            "cluster": None
        })
        assert result == '{node="node-01"}'

    def test_invalid_join_operator(self):
        """Test that invalid join operators are rejected."""
        with pytest.raises(PromQLValidationError, match="Invalid join operator"):
            build_label_filter({"node": "node-01"}, join_operator="INVALID")


class TestValidateStep:
    """Test step/duration validation."""

    def test_valid_seconds(self):
        """Test valid second durations."""
        assert validate_step("30s") == "30s"
        assert validate_step("60s") == "60s"

    def test_valid_minutes(self):
        """Test valid minute durations."""
        assert validate_step("5m") == "5m"
        assert validate_step("15m") == "15m"

    def test_valid_hours(self):
        """Test valid hour durations."""
        assert validate_step("1h") == "1h"
        assert validate_step("24h") == "24h"

    def test_valid_days(self):
        """Test valid day durations."""
        assert validate_step("7d") == "7d"

    def test_valid_milliseconds(self):
        """Test valid millisecond durations."""
        assert validate_step("100ms") == "100ms"

    def test_invalid_format(self):
        """Test that invalid formats are rejected."""
        with pytest.raises(PromQLValidationError, match="Invalid step format"):
            validate_step("invalid")

        with pytest.raises(PromQLValidationError, match="Invalid step format"):
            validate_step("5x")  # Invalid unit

    def test_invalid_empty(self):
        """Test that empty step is rejected."""
        with pytest.raises(PromQLValidationError, match="cannot be empty"):
            validate_step("")


class TestRealWorldInjectionAttempts:
    """Test against real-world PromQL injection patterns."""

    def test_label_escape_attempt(self):
        """Test blocking label escape injection."""
        malicious_input = 'node"}}or{job="malicious'
        with pytest.raises(PromQLValidationError):
            sanitize_label_value(malicious_input)

    def test_function_injection_attempt(self):
        """Test blocking function injection."""
        malicious_input = 'node")or(up{job="'
        with pytest.raises(PromQLValidationError):
            sanitize_label_value(malicious_input)

    def test_aggregation_injection_attempt(self):
        """Test blocking aggregation injection."""
        malicious_input = 'node"}}by(instance){{'
        with pytest.raises(PromQLValidationError):
            sanitize_label_value(malicious_input)

    def test_safe_build_label_filter_integration(self):
        """Test that build_label_filter properly sanitizes inputs."""
        # This should work fine
        result = build_label_filter({
            "exported_instance": "node-01",
            "cluster": "prod-cluster",
            "node": "192.168.1.1:9102"
        })
        assert "exported_instance=\"node-01\"" in result
        assert "cluster=\"prod-cluster\"" in result

        # This should raise an error
        with pytest.raises(PromQLValidationError):
            build_label_filter({
                "node": 'malicious"}}or{{'
            })
