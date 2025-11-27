"""
Hardware Domain Models - Physical hardware monitoring via IPMI.

This module contains data models for hardware resources including:
- IPMI sensor models (power, temperature, fan, voltage)
"""

from .ipmi import (
    IPMISensorType,
    IPMISensorStatus,
    IPMISensorData,
    IPMIPowerData,
    IPMITemperatureData,
    IPMIFanData,
    IPMIVoltageData,
    IPMISensorListResponse,
    IPMIPowerResponse,
    IPMITemperatureResponse,
    IPMIFanResponse,
    IPMISummaryResponse
)

__all__ = [
    # IPMI enums
    "IPMISensorType",
    "IPMISensorStatus",

    # IPMI sensor models
    "IPMISensorData",
    "IPMIPowerData",
    "IPMITemperatureData",
    "IPMIFanData",
    "IPMIVoltageData",

    # IPMI response models
    "IPMISensorListResponse",
    "IPMIPowerResponse",
    "IPMITemperatureResponse",
    "IPMIFanResponse",
    "IPMISummaryResponse"
]
