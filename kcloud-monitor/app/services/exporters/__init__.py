"""
Exporters Package - Data export functionality.

This package provides export functionality for various formats:
- CSV: Simple columnar data export
- Parquet: Apache Parquet columnar format
- Excel: Microsoft Excel format with formatting
- PDF: PDF reports with tables and styling
"""

from .csv_exporter import (
    export_to_csv,
    export_power_to_csv,
    export_metrics_to_csv,
    export_timeseries_to_csv
)


def _missing_dependency(package: str):
    """Return a stub exporter that raises a helpful ImportError."""
    def _raiser(*_args, **_kwargs):
        raise ImportError(f"{package} package is required for this export format. "
                          f"Install with: pip install {package}")
    return _raiser


# Defaults for optional exporters
PARQUET_AVAILABLE = False
EXCEL_AVAILABLE = False
PDF_AVAILABLE = False

try:
    from .parquet_exporter import (
        export_to_parquet,
        export_power_to_parquet,
        export_metrics_to_parquet,
        export_timeseries_to_parquet,
        PARQUET_AVAILABLE as _PARQUET_AVAILABLE
    )
    PARQUET_AVAILABLE = _PARQUET_AVAILABLE
except ImportError:
    export_to_parquet = _missing_dependency("pyarrow")
    export_power_to_parquet = _missing_dependency("pyarrow")
    export_metrics_to_parquet = _missing_dependency("pyarrow")
    export_timeseries_to_parquet = _missing_dependency("pyarrow")

try:
    from .excel_exporter import (
        export_to_excel,
        export_power_to_excel,
        export_metrics_to_excel,
        export_timeseries_to_excel,
        EXCEL_AVAILABLE as _EXCEL_AVAILABLE
    )
    EXCEL_AVAILABLE = _EXCEL_AVAILABLE
except ImportError:
    export_to_excel = _missing_dependency("openpyxl")
    export_power_to_excel = _missing_dependency("openpyxl")
    export_metrics_to_excel = _missing_dependency("openpyxl")
    export_timeseries_to_excel = _missing_dependency("openpyxl")

try:
    from .pdf_exporter import (
        export_to_pdf,
        export_power_to_pdf,
        export_metrics_to_pdf,
        generate_daily_report,
        generate_weekly_report,
        generate_monthly_report,
        PDF_AVAILABLE as _PDF_AVAILABLE
    )
    PDF_AVAILABLE = _PDF_AVAILABLE
except ImportError:
    export_to_pdf = _missing_dependency("reportlab")
    export_power_to_pdf = _missing_dependency("reportlab")
    export_metrics_to_pdf = _missing_dependency("reportlab")
    generate_daily_report = _missing_dependency("reportlab")
    generate_weekly_report = _missing_dependency("reportlab")
    generate_monthly_report = _missing_dependency("reportlab")

__all__ = [
    # CSV
    'export_to_csv',
    'export_power_to_csv',
    'export_metrics_to_csv',
    'export_timeseries_to_csv',

    # Parquet
    'export_to_parquet',
    'export_power_to_parquet',
    'export_metrics_to_parquet',
    'export_timeseries_to_parquet',
    'PARQUET_AVAILABLE',

    # Excel
    'export_to_excel',
    'export_power_to_excel',
    'export_metrics_to_excel',
    'export_timeseries_to_excel',
    'EXCEL_AVAILABLE',

    # PDF
    'export_to_pdf',
    'export_power_to_pdf',
    'export_metrics_to_pdf',
    'generate_daily_report',
    'generate_weekly_report',
    'generate_monthly_report',
    'PDF_AVAILABLE'
]
