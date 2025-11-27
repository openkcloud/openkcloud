"""
PDF Exporter - Export data to PDF format with reports.

This module provides PDF export functionality with formatting and charts.
Requires: reportlab
"""

import io
from typing import List, Dict, Any, Optional
from datetime import datetime

try:
    from reportlab.lib.pagesizes import letter, A4
    from reportlab.lib import colors
    from reportlab.lib.units import inch
    from reportlab.platypus import SimpleDocTemplate, Table, TableStyle, Paragraph, Spacer, PageBreak
    from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
    from reportlab.lib.enums import TA_CENTER, TA_LEFT, TA_RIGHT
    PDF_AVAILABLE = True
except ImportError:
    PDF_AVAILABLE = False


def export_to_pdf(
    title: str,
    data: List[Dict[str, Any]],
    columns: Optional[List[str]] = None,
    page_size=None
) -> bytes:
    """
    Export data to PDF format.

    Args:
        title: Document title
        data: List of dictionaries to export
        columns: Column names (if None, uses keys from first row)
        page_size: Page size (letter or A4)

    Returns:
        PDF file bytes

    Raises:
        ImportError: If reportlab is not installed
    """
    if not PDF_AVAILABLE:
        raise ImportError("reportlab is not installed. Install with: pip install reportlab")

    # Set default page size
    if page_size is None:
        page_size = letter

    # Create PDF buffer
    buffer = io.BytesIO()
    doc = SimpleDocTemplate(buffer, pagesize=page_size)
    story = []

    # Styles
    styles = getSampleStyleSheet()
    title_style = ParagraphStyle(
        'CustomTitle',
        parent=styles['Heading1'],
        fontSize=24,
        textColor=colors.HexColor('#1f4788'),
        spaceAfter=30,
        alignment=TA_CENTER
    )

    # Add title
    story.append(Paragraph(title, title_style))
    story.append(Spacer(1, 0.2 * inch))

    if not data:
        story.append(Paragraph("No data available", styles['Normal']))
        doc.build(story)
        buffer.seek(0)
        return buffer.getvalue()

    # Determine columns
    if columns is None:
        columns = list(data[0].keys())

    # Create table data
    table_data = [columns]  # Header row
    for row in data:
        table_data.append([str(row.get(col, '')) for col in columns])

    # Create table
    table = Table(table_data)
    table.setStyle(TableStyle([
        # Header styling
        ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#4472C4')),
        ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
        ('ALIGN', (0, 0), (-1, 0), 'CENTER'),
        ('FONTNAME', (0, 0), (-1, 0), 'Helvetica-Bold'),
        ('FONTSIZE', (0, 0), (-1, 0), 12),
        ('BOTTOMPADDING', (0, 0), (-1, 0), 12),

        # Data styling
        ('BACKGROUND', (0, 1), (-1, -1), colors.beige),
        ('TEXTCOLOR', (0, 1), (-1, -1), colors.black),
        ('ALIGN', (0, 1), (-1, -1), 'LEFT'),
        ('FONTNAME', (0, 1), (-1, -1), 'Helvetica'),
        ('FONTSIZE', (0, 1), (-1, -1), 10),
        ('ROWBACKGROUNDS', (0, 1), (-1, -1), [colors.white, colors.lightgrey]),

        # Grid
        ('GRID', (0, 0), (-1, -1), 1, colors.black)
    ]))

    story.append(table)

    # Build PDF
    doc.build(story)
    buffer.seek(0)

    return buffer.getvalue()


def export_power_to_pdf(power_data: Dict[str, Any], report_type: str = "summary") -> bytes:
    """
    Export power monitoring data to PDF format with formatted report.

    Args:
        power_data: Power data dictionary
        report_type: Report type (summary, detailed, breakdown)

    Returns:
        PDF file bytes
    """
    if not PDF_AVAILABLE:
        raise ImportError("reportlab is not installed. Install with: pip install reportlab")

    buffer = io.BytesIO()
    doc = SimpleDocTemplate(buffer, pagesize=letter)
    story = []

    styles = getSampleStyleSheet()
    title_style = ParagraphStyle(
        'CustomTitle',
        parent=styles['Heading1'],
        fontSize=24,
        textColor=colors.HexColor('#1f4788'),
        spaceAfter=30,
        alignment=TA_CENTER
    )
    heading_style = ParagraphStyle(
        'CustomHeading',
        parent=styles['Heading2'],
        fontSize=16,
        textColor=colors.HexColor('#2e5c8a'),
        spaceAfter=12,
        spaceBefore=12
    )

    # Title
    timestamp = power_data.get('timestamp', datetime.utcnow().isoformat())
    story.append(Paragraph("Power Consumption Report", title_style))
    story.append(Paragraph(f"Generated: {timestamp}", styles['Normal']))
    story.append(Spacer(1, 0.3 * inch))

    # Summary Section
    if 'summary' in power_data:
        story.append(Paragraph("Power Summary", heading_style))
        summary = power_data['summary']

        summary_data = [
            ["Metric", "Value", "Unit"],
            ["Total Power", f"{summary.get('total_power_watts', 0):.2f}", "Watts"],
            ["Resource Count", str(summary.get('resource_count', 0)), ""],
            ["Average Power", f"{summary.get('avg_power_watts', 0):.2f}", "Watts"],
            ["Maximum Power", f"{summary.get('max_power_watts', 0):.2f}", "Watts"],
            ["Minimum Power", f"{summary.get('min_power_watts', 0):.2f}", "Watts"]
        ]

        summary_table = Table(summary_data, colWidths=[2.5*inch, 2*inch, 1.5*inch])
        summary_table.setStyle(TableStyle([
            ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#4472C4')),
            ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
            ('ALIGN', (0, 0), (-1, 0), 'CENTER'),
            ('FONTNAME', (0, 0), (-1, 0), 'Helvetica-Bold'),
            ('FONTSIZE', (0, 0), (-1, 0), 12),
            ('BACKGROUND', (0, 1), (-1, -1), colors.white),
            ('ALIGN', (1, 1), (-1, -1), 'RIGHT'),
            ('GRID', (0, 0), (-1, -1), 1, colors.black)
        ]))

        story.append(summary_table)
        story.append(Spacer(1, 0.3 * inch))

    # Breakdown Section
    if 'breakdown' in power_data and report_type in ['detailed', 'breakdown']:
        story.append(Paragraph("Power Breakdown", heading_style))

        breakdown_data = [["Name", "Power (W)", "Percentage", "Resource Count", "Type"]]
        for item in power_data['breakdown']:
            breakdown_data.append([
                str(item.get('name', '')),
                f"{item.get('power_watts', 0):.2f}",
                f"{item.get('percentage', 0):.1f}%",
                str(item.get('resource_count', 0)),
                str(item.get('resource_type', ''))
            ])

        breakdown_table = Table(breakdown_data, colWidths=[1.8*inch, 1.3*inch, 1.3*inch, 1.3*inch, 1.3*inch])
        breakdown_table.setStyle(TableStyle([
            ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#4472C4')),
            ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
            ('ALIGN', (0, 0), (-1, 0), 'CENTER'),
            ('FONTNAME', (0, 0), (-1, 0), 'Helvetica-Bold'),
            ('FONTSIZE', (0, 0), (-1, 0), 10),
            ('BACKGROUND', (0, 1), (-1, -1), colors.white),
            ('ALIGN', (1, 1), (-1, -1), 'RIGHT'),
            ('ROWBACKGROUNDS', (0, 1), (-1, -1), [colors.white, colors.lightgrey]),
            ('GRID', (0, 0), (-1, -1), 1, colors.black)
        ]))

        story.append(breakdown_table)
        story.append(Spacer(1, 0.3 * inch))

    # Accelerators Section
    if 'accelerators' in power_data and report_type == 'detailed':
        story.append(Paragraph("Accelerators (GPU/NPU)", heading_style))
        acc = power_data['accelerators']

        acc_data = [
            ["Metric", "Value", "Unit"],
            ["Total Power", f"{acc.get('total_power_watts', 0):.2f}", "Watts"],
            ["GPU Count", str(acc.get('gpu_count', 0)), ""],
            ["NPU Count", str(acc.get('npu_count', 0)), ""],
            ["Avg GPU Power", f"{acc.get('avg_gpu_power_watts', 0):.2f}", "Watts"]
        ]

        acc_table = Table(acc_data, colWidths=[2.5*inch, 2*inch, 1.5*inch])
        acc_table.setStyle(TableStyle([
            ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#4472C4')),
            ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
            ('ALIGN', (0, 0), (-1, 0), 'CENTER'),
            ('FONTNAME', (0, 0), (-1, 0), 'Helvetica-Bold'),
            ('BACKGROUND', (0, 1), (-1, -1), colors.white),
            ('ALIGN', (1, 1), (-1, -1), 'RIGHT'),
            ('GRID', (0, 0), (-1, -1), 1, colors.black)
        ]))

        story.append(acc_table)
        story.append(Spacer(1, 0.3 * inch))

    # Infrastructure Section
    if 'infrastructure' in power_data and report_type == 'detailed':
        story.append(Paragraph("Infrastructure (Nodes/Pods)", heading_style))
        infra = power_data['infrastructure']

        infra_data = [
            ["Metric", "Value", "Unit"],
            ["Total Power", f"{infra.get('total_power_watts', 0):.2f}", "Watts"],
            ["Node Count", str(infra.get('node_count', 0)), ""],
            ["Pod Count", str(infra.get('pod_count', 0)), ""],
            ["Avg Node Power", f"{infra.get('avg_node_power_watts', 0):.2f}", "Watts"]
        ]

        infra_table = Table(infra_data, colWidths=[2.5*inch, 2*inch, 1.5*inch])
        infra_table.setStyle(TableStyle([
            ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#4472C4')),
            ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
            ('ALIGN', (0, 0), (-1, 0), 'CENTER'),
            ('FONTNAME', (0, 0), (-1, 0), 'Helvetica-Bold'),
            ('BACKGROUND', (0, 1), (-1, -1), colors.white),
            ('ALIGN', (1, 1), (-1, -1), 'RIGHT'),
            ('GRID', (0, 0), (-1, -1), 1, colors.black)
        ]))

        story.append(infra_table)

    # Footer
    story.append(Spacer(1, 0.5 * inch))
    footer_style = ParagraphStyle(
        'Footer',
        parent=styles['Normal'],
        fontSize=8,
        textColor=colors.grey,
        alignment=TA_CENTER
    )
    story.append(Paragraph(
        "AI Accelerator & Infrastructure Monitoring API | Generated by FastAPI Server",
        footer_style
    ))

    # Build PDF
    doc.build(story)
    buffer.seek(0)

    return buffer.getvalue()


def export_metrics_to_pdf(metrics_data: Dict[str, Any]) -> bytes:
    """
    Export performance metrics data to PDF format.

    Args:
        metrics_data: Metrics data dictionary

    Returns:
        PDF file bytes
    """
    rows = []
    timestamp = metrics_data.get('timestamp', datetime.utcnow().isoformat())

    if 'metrics' in metrics_data:
        for metric in metrics_data['metrics']:
            rows.append({
                'Resource ID': metric.get('resource_id', ''),
                'Type': metric.get('resource_type', ''),
                'Metric': metric.get('metric_name', ''),
                'Value': f"{metric.get('value', 0):.2f}",
                'Unit': metric.get('unit', ''),
                'Status': metric.get('status', '')
            })

    return export_to_pdf(f"Performance Metrics Report - {timestamp}", rows)


def generate_daily_report(data: Dict[str, Any]) -> bytes:
    """
    Generate daily power consumption report.

    Args:
        data: Aggregated daily data

    Returns:
        PDF file bytes
    """
    return export_power_to_pdf(data, report_type="detailed")


def generate_weekly_report(data: Dict[str, Any]) -> bytes:
    """
    Generate weekly power consumption report.

    Args:
        data: Aggregated weekly data

    Returns:
        PDF file bytes
    """
    return export_power_to_pdf(data, report_type="detailed")


def generate_monthly_report(data: Dict[str, Any]) -> bytes:
    """
    Generate monthly power consumption report.

    Args:
        data: Aggregated monthly data

    Returns:
        PDF file bytes
    """
    return export_power_to_pdf(data, report_type="detailed")
