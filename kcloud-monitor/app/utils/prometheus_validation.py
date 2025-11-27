"""
PromQL Input Validation and Sanitization

This module provides security functions to prevent PromQL injection attacks
by validating and sanitizing user inputs before they are used in Prometheus queries.
"""

import re
from typing import Optional


class PromQLValidationError(ValueError):
    """Exception raised when PromQL input validation fails."""
    pass


def sanitize_label_value(value: str, max_length: int = 255) -> str:
    """
    Sanitize and validate a Prometheus label value.

    Label values in Prometheus can contain:
    - Alphanumeric characters (a-z, A-Z, 0-9)
    - Hyphens (-)
    - Underscores (_)
    - Periods (.)
    - Colons (:) for common patterns like IP:port
    - Forward slashes (/) for paths

    Args:
        value: The label value to sanitize
        max_length: Maximum allowed length (default: 255)

    Returns:
        The sanitized label value

    Raises:
        PromQLValidationError: If the value contains invalid characters or is too long

    Examples:
        >>> sanitize_label_value("node-01")
        'node-01'
        >>> sanitize_label_value("192.168.1.1:9102")
        '192.168.1.1:9102'
        >>> sanitize_label_value("malicious\"})")  # Raises PromQLValidationError
    """
    if not value:
        raise PromQLValidationError("Label value cannot be empty")

    if len(value) > max_length:
        raise PromQLValidationError(
            f"Label value exceeds maximum length of {max_length}: {value[:50]}..."
        )

    # Allow alphanumeric, hyphens, underscores, periods, colons, and forward slashes
    # This pattern covers common use cases like:
    # - Hostnames: node-01, server.example.com
    # - IP addresses: 192.168.1.1, 10.0.0.1:9102
    # - Paths: /var/lib/data
    # - UUIDs: 550e8400-e29b-41d4-a716-446655440000
    if not re.match(r'^[a-zA-Z0-9\-_.:/]+$', value):
        raise PromQLValidationError(
            f"Label value contains invalid characters. "
            f"Allowed: alphanumeric, hyphen, underscore, period, colon, slash. "
            f"Got: {value}"
        )

    # Additional security checks for common injection patterns
    dangerous_patterns = [
        r'["\']',      # Quotes (used to escape label values)
        r'[{}]',       # Braces (used in label matchers)
        r'[()]',       # Parentheses (used in PromQL functions)
        r'[;]',        # Semicolons
        r'[\s]',       # Whitespace (should not be in label values)
        r'[\\]',       # Backslashes (escape characters)
    ]

    for pattern in dangerous_patterns:
        if re.search(pattern, value):
            raise PromQLValidationError(
                f"Label value contains potentially dangerous characters: {value}"
            )

    return value


def sanitize_metric_name(metric_name: str) -> str:
    """
    Sanitize and validate a Prometheus metric name.

    Metric names must match: [a-zA-Z_:][a-zA-Z0-9_:]*

    Args:
        metric_name: The metric name to sanitize

    Returns:
        The sanitized metric name

    Raises:
        PromQLValidationError: If the metric name is invalid

    Examples:
        >>> sanitize_metric_name("kepler_node_power")
        'kepler_node_power'
        >>> sanitize_metric_name("cpu:usage:rate5m")
        'cpu:usage:rate5m'
        >>> sanitize_metric_name("123invalid")  # Raises PromQLValidationError
    """
    if not metric_name:
        raise PromQLValidationError("Metric name cannot be empty")

    if len(metric_name) > 255:
        raise PromQLValidationError(
            f"Metric name exceeds maximum length of 255: {metric_name[:50]}..."
        )

    # Prometheus metric naming convention:
    # - Must start with [a-zA-Z_:]
    # - Followed by [a-zA-Z0-9_:]*
    if not re.match(r'^[a-zA-Z_:][a-zA-Z0-9_:]*$', metric_name):
        raise PromQLValidationError(
            f"Invalid metric name format. Must match [a-zA-Z_:][a-zA-Z0-9_:]*. "
            f"Got: {metric_name}"
        )

    return metric_name


def sanitize_label_name(label_name: str) -> str:
    """
    Sanitize and validate a Prometheus label name.

    Label names must match: [a-zA-Z_][a-zA-Z0-9_]*

    Args:
        label_name: The label name to sanitize

    Returns:
        The sanitized label name

    Raises:
        PromQLValidationError: If the label name is invalid

    Examples:
        >>> sanitize_label_name("exported_instance")
        'exported_instance'
        >>> sanitize_label_name("node_name")
        'node_name'
        >>> sanitize_label_name("123invalid")  # Raises PromQLValidationError
    """
    if not label_name:
        raise PromQLValidationError("Label name cannot be empty")

    if len(label_name) > 255:
        raise PromQLValidationError(
            f"Label name exceeds maximum length of 255: {label_name[:50]}..."
        )

    # Prometheus label naming convention:
    # - Must start with [a-zA-Z_]
    # - Followed by [a-zA-Z0-9_]*
    # - Labels starting with __ are reserved for internal use
    if not re.match(r'^[a-zA-Z_][a-zA-Z0-9_]*$', label_name):
        raise PromQLValidationError(
            f"Invalid label name format. Must match [a-zA-Z_][a-zA-Z0-9_]*. "
            f"Got: {label_name}"
        )

    if label_name.startswith('__'):
        raise PromQLValidationError(
            f"Label names starting with '__' are reserved for internal use: {label_name}"
        )

    return label_name


def build_label_matcher(label_name: str, label_value: str, operator: str = "=") -> str:
    """
    Build a safe label matcher for PromQL queries.

    Args:
        label_name: The label name (e.g., "exported_instance")
        label_value: The label value (e.g., "node-01")
        operator: The matcher operator (=, !=, =~, !~). Default: "="

    Returns:
        A safe label matcher string (e.g., 'exported_instance="node-01"')

    Raises:
        PromQLValidationError: If inputs are invalid

    Examples:
        >>> build_label_matcher("node", "node-01")
        'node="node-01"'
        >>> build_label_matcher("instance", "192.168.1.1:9102", "=~")
        'instance=~"192.168.1.1:9102"'
    """
    # Validate inputs
    label_name = sanitize_label_name(label_name)
    label_value = sanitize_label_value(label_value)

    # Validate operator
    valid_operators = {"=", "!=", "=~", "!~"}
    if operator not in valid_operators:
        raise PromQLValidationError(
            f"Invalid operator. Must be one of {valid_operators}. Got: {operator}"
        )

    # Build the matcher
    return f'{label_name}{operator}"{label_value}"'


def build_label_filter(filters: dict, join_operator: str = ",") -> str:
    """
    Build a safe label filter string from a dictionary of label matchers.

    Args:
        filters: Dictionary of label_name -> label_value
        join_operator: Operator to join filters ("," for AND, "|" for OR)

    Returns:
        A safe label filter string (e.g., '{node="node-01",cluster="prod"}')

    Raises:
        PromQLValidationError: If inputs are invalid

    Examples:
        >>> build_label_filter({"node": "node-01", "cluster": "prod"})
        '{node="node-01",cluster="prod"}'
        >>> build_label_filter({})
        ''
    """
    if not filters:
        return ""

    # Validate join operator
    if join_operator not in {",", "|"}:
        raise PromQLValidationError(
            f"Invalid join operator. Must be ',' (AND) or '|' (OR). Got: {join_operator}"
        )

    # Build matchers
    matchers = []
    for label_name, label_value in filters.items():
        if label_value is not None:  # Skip None values
            matcher = build_label_matcher(label_name, label_value)
            matchers.append(matcher)

    if not matchers:
        return ""

    return "{" + join_operator.join(matchers) + "}"


def validate_step(step: str) -> str:
    """
    Validate a Prometheus step/duration value.

    Step must match: [0-9]+(ms|s|m|h|d|w|y)

    Args:
        step: The step value (e.g., "5m", "1h", "30s")

    Returns:
        The validated step value

    Raises:
        PromQLValidationError: If the step format is invalid

    Examples:
        >>> validate_step("5m")
        '5m'
        >>> validate_step("1h")
        '1h'
        >>> validate_step("invalid")  # Raises PromQLValidationError
    """
    if not step:
        raise PromQLValidationError("Step value cannot be empty")

    # Prometheus duration format: [0-9]+(ms|s|m|h|d|w|y)
    if not re.match(r'^[0-9]+(ms|s|m|h|d|w|y)$', step):
        raise PromQLValidationError(
            f"Invalid step format. Must match [0-9]+(ms|s|m|h|d|w|y). Got: {step}"
        )

    return step
