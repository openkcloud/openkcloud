from datetime import datetime
from enum import Enum
from pydantic import BaseModel, Field

class TimePeriod(str, Enum):
    """Supported time periods for queries."""
    HOUR = "1h"
    DAY = "1d"
    WEEK = "1w"
    MONTH = "1m"

class DataFormat(str, Enum):
    """Supported data export formats."""
    JSON = "json"
    CSV = "csv"

class BaseResponse(BaseModel):
    """Base model included in all API responses."""
    timestamp: datetime = Field(default_factory=datetime.utcnow, description="Timestamp of the response in UTC.")
