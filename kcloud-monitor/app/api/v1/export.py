"""
Export API - Data export endpoints.

This module provides endpoints for:
- Power data export (JSON, CSV, Parquet, Excel)
- Metrics data export
- Comprehensive report generation
"""

from fastapi import APIRouter, Depends, HTTPException, Query, Response
from fastapi.responses import JSONResponse
from typing import Optional
from datetime import datetime
import logging

# Authentication handled at router level in main.py
from app.models.queries import ExportQueryParams, GPUQueryParams
from app.services import cache_service
from app import crud

# Import exporters
from app.services.exporters import (
    export_power_to_csv,
    export_metrics_to_csv,
    export_timeseries_to_csv,
    export_power_to_parquet,
    export_metrics_to_parquet,
    export_timeseries_to_parquet,
    export_power_to_excel,
    export_metrics_to_excel,
    export_timeseries_to_excel,
    export_power_to_pdf,
    export_metrics_to_pdf,
    generate_daily_report,
    generate_weekly_report,
    generate_monthly_report,
    PARQUET_AVAILABLE,
    EXCEL_AVAILABLE,
    PDF_AVAILABLE
)

logger = logging.getLogger(__name__)

router = APIRouter()

# ============================================================================
# Power Data Export
# ============================================================================

@router.get("/export/power",
           summary="Export power data",
           description="Export power consumption data in various formats.")
async def export_power_data(
    format: str = Query("json", description="Export format (json/csv/parquet/excel/pdf)"),
    period: str = Query("1h", description="Time period (1h/1d/1w/1m)"),
    resource_type: Optional[str] = Query(None, description="Resource type (accelerators/infrastructure/all)"),
    breakdown_by: Optional[str] = Query(None, description="Breakdown dimension (cluster/node/namespace/vendor/resource_type)"),
    cluster: Optional[str] = Query(None, description="Cluster filter")
):
    """
    Export power consumption data in various formats.

    **Query Parameters:**
    - `format`: Export format (json/csv/parquet/excel/pdf) [Default: json]
    - `period`: Time period (1h/1d/1w/1m) [Default: 1h]
    - `resource_type`: Resource type filter (accelerators/infrastructure/all)
    - `breakdown_by`: Breakdown dimension (cluster/node/namespace/vendor/resource_type)
    - `cluster`: Cluster filter

    **Supported Formats:**
    - `json`: JSON format (default)
    - `csv`: Comma-separated values
    - `parquet`: Apache Parquet (requires pyarrow)
    - `excel`: Microsoft Excel XLSX (requires openpyxl)
    - `pdf`: PDF report (requires reportlab)

    **Returns:** Power data in requested format with appropriate Content-Type header.

    **Response Headers:**
    - `Content-Type`: Format-specific MIME type
    - `Content-Disposition`: attachment; filename=power_export_YYYYMMDDHHMMSS.{format}
    """
    try:
        # Validate format
        format_lower = format.lower()
        if format_lower not in ['json', 'csv', 'parquet', 'excel', 'pdf']:
            raise HTTPException(status_code=400, detail=f"Unsupported format: {format}")

        # Check availability
        if format_lower == 'parquet' and not PARQUET_AVAILABLE:
            raise HTTPException(status_code=400, detail="Parquet export requires 'pyarrow' package. Install with: pip install pyarrow")
        if format_lower == 'excel' and not EXCEL_AVAILABLE:
            raise HTTPException(status_code=400, detail="Excel export requires 'openpyxl' package. Install with: pip install openpyxl")
        if format_lower == 'pdf' and not PDF_AVAILABLE:
            raise HTTPException(status_code=400, detail="PDF export requires 'reportlab' package. Install with: pip install reportlab")

        # Get power data based on parameters
        if breakdown_by:
            # Get breakdown data
            data = await crud.get_power_breakdown(breakdown_by=breakdown_by, cluster=cluster)
        elif resource_type == 'accelerators':
            data = await crud.get_accelerator_power(cluster=cluster)
        elif resource_type == 'infrastructure':
            data = await crud.get_infrastructure_power(cluster=cluster)
        else:
            # Get unified power (default)
            data = await crud.get_unified_power(cluster=cluster)

        # Generate export content based on format
        if format_lower == 'json':
            content = data
            media_type = "application/json"
            # Return JSON response directly
            return JSONResponse(
                content=content,
                headers={"Content-Disposition": f"attachment; filename=power_export_{datetime.utcnow().strftime('%Y%m%d%H%M%S')}.json"}
            )

        elif format_lower == 'csv':
            content = export_power_to_csv(data)
            media_type = "text/csv"

        elif format_lower == 'parquet':
            content = export_power_to_parquet(data)
            media_type = "application/octet-stream"

        elif format_lower == 'excel':
            content = export_power_to_excel(data)
            media_type = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

        elif format_lower == 'pdf':
            content = export_power_to_pdf(data, report_type="detailed" if breakdown_by else "summary")
            media_type = "application/pdf"

        # Generate filename
        filename = f"power_export_{datetime.utcnow().strftime('%Y%m%d%H%M%S')}.{format_lower}"

        return Response(
            content=content,
            media_type=media_type,
            headers={"Content-Disposition": f"attachment; filename={filename}"}
        )

    except HTTPException:
        raise
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logger.error(f"Failed to export power data: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to export power data: {str(e)}")


# ============================================================================
# Metrics Data Export
# ============================================================================

@router.get("/export/metrics",
           summary="Export metrics data",
           description="Export performance metrics data in various formats.")
async def export_metrics_data(
    format: str = Query("json", description="Export format (json/csv/parquet/excel/pdf)"),
    metric_name: Optional[str] = Query(None, description="Metric name (utilization/temperature/memory_usage)"),
    resource_type: Optional[str] = Query(None, description="Resource type (gpus/npus/nodes)"),
    period: str = Query("1h", description="Time period (1h/1d/1w/1m)"),
    step: Optional[str] = Query("5m", description="Step interval (1m/5m/15m/30m/1h)"),
    cluster: Optional[str] = Query(None, description="Cluster filter")
):
    """
    Export performance metrics data in various formats.

    **Query Parameters:**
    - `format`: Export format (json/csv/parquet/excel/pdf) [Default: json]
    - `metric_name`: Metric name (utilization/temperature/memory_usage)
    - `resource_type`: Resource type (gpus/npus/nodes)
    - `period`: Time period (1h/1d/1w/1m) [Default: 1h]
    - `step`: Step interval (1m/5m/15m/30m/1h) [Default: 5m]
    - `cluster`: Cluster filter

    **Supported Formats:**
    - `json`: JSON format (default)
    - `csv`: Comma-separated values
    - `parquet`: Apache Parquet (requires pyarrow)
    - `excel`: Microsoft Excel XLSX (requires openpyxl)
    - `pdf`: PDF report (requires reportlab)

    **Returns:** Metrics data in requested format with appropriate Content-Type header.

    **Response Headers:**
    - `Content-Type`: Format-specific MIME type
    - `Content-Disposition`: attachment; filename=metrics_export_YYYYMMDDHHMMSS.{format}
    """
    try:
        # Validate format
        format_lower = format.lower()
        if format_lower not in ['json', 'csv', 'parquet', 'excel', 'pdf']:
            raise HTTPException(status_code=400, detail=f"Unsupported format: {format}")

        # Check availability
        if format_lower == 'parquet' and not PARQUET_AVAILABLE:
            raise HTTPException(status_code=400, detail="Parquet export requires 'pyarrow' package")
        if format_lower == 'excel' and not EXCEL_AVAILABLE:
            raise HTTPException(status_code=400, detail="Excel export requires 'openpyxl' package")
        if format_lower == 'pdf' and not PDF_AVAILABLE:
            raise HTTPException(status_code=400, detail="PDF export requires 'reportlab' package")

        # Get metrics timeseries data
        if metric_name:
            data = await crud.get_metrics_timeseries(
                metric_name=metric_name,
                resource_type=resource_type,
                period=period,
                step=step
            )
        else:
            # Default to utilization metrics
            data = await crud.get_metrics_timeseries(
                metric_name="utilization",
                resource_type=resource_type or "gpus",
                period=period,
                step=step
            )

        # Generate export content based on format
        if format_lower == 'json':
            return JSONResponse(
                content=data,
                headers={"Content-Disposition": f"attachment; filename=metrics_export_{datetime.utcnow().strftime('%Y%m%d%H%M%S')}.json"}
            )

        elif format_lower == 'csv':
            content = export_timeseries_to_csv(data)
            media_type = "text/csv"

        elif format_lower == 'parquet':
            content = export_timeseries_to_parquet(data)
            media_type = "application/octet-stream"

        elif format_lower == 'excel':
            content = export_timeseries_to_excel(data)
            media_type = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

        elif format_lower == 'pdf':
            content = export_metrics_to_pdf(data)
            media_type = "application/pdf"

        # Generate filename
        filename = f"metrics_export_{datetime.utcnow().strftime('%Y%m%d%H%M%S')}.{format_lower}"

        return Response(
            content=content,
            media_type=media_type,
            headers={"Content-Disposition": f"attachment; filename={filename}"}
        )

    except HTTPException:
        raise
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logger.error(f"Failed to export metrics data: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to export metrics data: {str(e)}")


# ============================================================================
# Report Generation
# ============================================================================

@router.get("/export/report",
           summary="Generate comprehensive report",
           description="Generate comprehensive reports with data aggregation and analysis.")
async def generate_report(
    template: str = Query("daily", description="Report template (daily/weekly/monthly/custom)"),
    format: str = Query("pdf", description="Report format (pdf/excel)"),
    cluster: Optional[str] = Query(None, description="Cluster filter"),
    start_date: Optional[str] = Query(None, description="Start date for custom reports (YYYY-MM-DD)"),
    end_date: Optional[str] = Query(None, description="End date for custom reports (YYYY-MM-DD)")
):
    """
    Generate comprehensive monitoring reports.

    **Query Parameters:**
    - `template`: Report template [Default: daily]
      - `daily`: Daily summary report (24 hours)
      - `weekly`: Weekly trend report (7 days)
      - `monthly`: Monthly analysis report (30 days)
      - `custom`: Custom date range report (requires start_date/end_date)
    - `format`: Report format (pdf/excel) [Default: pdf]
    - `cluster`: Cluster filter
    - `start_date`: Start date for custom reports (YYYY-MM-DD)
    - `end_date`: End date for custom reports (YYYY-MM-DD)

    **Report Contents:**
    - Power consumption summary and trends
    - Resource utilization statistics
    - Accelerator metrics (GPU/NPU)
    - Infrastructure metrics (Nodes/Pods)
    - Temperature monitoring
    - Efficiency metrics (PUE)

    **Returns:** Generated report in requested format.

    **Response Headers:**
    - `Content-Type`: application/pdf or application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
    - `Content-Disposition`: attachment; filename=report_{template}_{date}.{format}
    """
    try:
        # Validate template
        template_lower = template.lower()
        if template_lower not in ['daily', 'weekly', 'monthly', 'custom']:
            raise HTTPException(status_code=400, detail=f"Unsupported template: {template}")

        # Validate format
        format_lower = format.lower()
        if format_lower not in ['pdf', 'excel']:
            raise HTTPException(status_code=400, detail=f"Unsupported format: {format}. Use 'pdf' or 'excel'")

        # Check availability
        if format_lower == 'pdf' and not PDF_AVAILABLE:
            raise HTTPException(status_code=400, detail="PDF reports require 'reportlab' package")
        if format_lower == 'excel' and not EXCEL_AVAILABLE:
            raise HTTPException(status_code=400, detail="Excel reports require 'openpyxl' package")

        # Validate custom template parameters
        if template_lower == 'custom':
            if not start_date or not end_date:
                raise HTTPException(status_code=400, detail="Custom template requires start_date and end_date parameters")

        # Determine period based on template
        period_map = {
            'daily': '1d',
            'weekly': '1w',
            'monthly': '1M',
            'custom': None  # Will use start_date/end_date
        }
        period = period_map.get(template_lower, '1d')

        # Collect report data
        # 1. Unified power data
        power_data = await crud.get_unified_power(cluster=cluster)

        # 2. Power efficiency
        efficiency_data = await crud.get_power_efficiency(cluster=cluster)

        # 3. Accelerator summary
        try:
            gpu_summary = await crud.get_gpu_summary()
            power_data['gpu_summary'] = gpu_summary
        except Exception as e:
            logger.warning(f"Failed to get GPU summary: {e}")

        # 4. Add efficiency metrics to power data
        if efficiency_data:
            power_data['efficiency'] = efficiency_data.get('efficiency', {})

        # 5. Add report metadata
        power_data['report_metadata'] = {
            'template': template_lower,
            'period': period,
            'cluster': cluster,
            'generated_at': datetime.utcnow().isoformat()
        }

        # Generate report based on template and format
        if format_lower == 'pdf':
            if template_lower == 'daily':
                content = generate_daily_report(power_data)
            elif template_lower == 'weekly':
                content = generate_weekly_report(power_data)
            elif template_lower == 'monthly':
                content = generate_monthly_report(power_data)
            else:  # custom
                content = export_power_to_pdf(power_data, report_type="detailed")

            media_type = "application/pdf"

        elif format_lower == 'excel':
            content = export_power_to_excel(power_data)
            media_type = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

        # Generate filename
        date_str = datetime.utcnow().strftime('%Y%m%d')
        filename = f"report_{template_lower}_{date_str}.{format_lower}"

        return Response(
            content=content,
            media_type=media_type,
            headers={"Content-Disposition": f"attachment; filename={filename}"}
        )

    except HTTPException:
        raise
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logger.error(f"Failed to generate report: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to generate report: {str(e)}")


# ============================================================================
# Export Status and History
# ============================================================================

@router.get("/export/formats",
           summary="Get supported export formats",
           description="Get list of supported export formats and their capabilities.")
async def get_export_formats():
    """
    Get list of supported export formats and their capabilities.

    **Returns:** List of export formats with descriptions and status.
    """
    return {
        "formats": {
            "json": {
                "name": "JSON",
                "description": "JavaScript Object Notation - human-readable structured data",
                "mime_type": "application/json",
                "file_extension": ".json",
                "status": "implemented",
                "features": ["timeseries", "nested_data", "metadata"]
            },
            "csv": {
                "name": "CSV",
                "description": "Comma-Separated Values - simple tabular data",
                "mime_type": "text/csv",
                "file_extension": ".csv",
                "status": "implemented",
                "features": ["timeseries", "flat_data"]
            },
            "parquet": {
                "name": "Apache Parquet",
                "description": "Columnar storage format - efficient for analytics",
                "mime_type": "application/octet-stream",
                "file_extension": ".parquet",
                "status": "implemented" if PARQUET_AVAILABLE else "requires_package",
                "package": "pyarrow",
                "install": "pip install pyarrow",
                "features": ["timeseries", "compression", "schema"]
            },
            "excel": {
                "name": "Microsoft Excel",
                "description": "Excel workbook - multiple sheets with formatting",
                "mime_type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
                "file_extension": ".xlsx",
                "status": "implemented" if EXCEL_AVAILABLE else "requires_package",
                "package": "openpyxl",
                "install": "pip install openpyxl",
                "features": ["timeseries", "multiple_sheets", "formatting", "charts"]
            },
            "pdf": {
                "name": "PDF",
                "description": "Portable Document Format - formatted reports",
                "mime_type": "application/pdf",
                "file_extension": ".pdf",
                "status": "implemented" if PDF_AVAILABLE else "requires_package",
                "package": "reportlab",
                "install": "pip install reportlab",
                "features": ["report", "formatting", "charts", "images"]
            },
            "html": {
                "name": "HTML",
                "description": "HTML report - interactive web-based reports",
                "mime_type": "text/html",
                "file_extension": ".html",
                "status": "not_implemented",
                "phase": "Phase 8.2",
                "features": ["report", "interactive", "charts", "styling"]
            }
        },
        "report_templates": {
            "daily": {
                "name": "Daily Summary",
                "description": "24-hour summary with hourly breakdown",
                "status": "implemented"
            },
            "weekly": {
                "name": "Weekly Trend",
                "description": "7-day trends and analysis",
                "status": "implemented"
            },
            "monthly": {
                "name": "Monthly Analysis",
                "description": "30-day comprehensive analysis",
                "status": "implemented"
            },
            "custom": {
                "name": "Custom Range",
                "description": "User-defined date range report",
                "status": "implemented"
            }
        }
    }


@router.get("/export/templates",
           summary="Get report templates",
           description="Get list of available report templates.")
async def get_report_templates():
    """
    Get list of available report templates.

    **Status:** Phase 8 - To be implemented

    **Returns:** List of report templates with descriptions.
    """
    # Return same data as /export/formats but focused on templates
    formats_data = await get_export_formats()
    return {
        "timestamp": datetime.utcnow(),
        "templates": formats_data["report_templates"]
    }
