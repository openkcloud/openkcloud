"""
Collectors Package - Data collection services for various monitoring sources.

This package contains collector modules for:
- IPMI: Physical hardware sensors (power, temperature, fans, voltage)
- OpenStack: Virtual machine monitoring (future implementation)
- NPU: Neural Processing Unit metrics (future implementation)
"""

from app.services.collectors.ipmi import IPMICollector

__all__ = ["IPMICollector"]
