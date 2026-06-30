"""Tests for the SSE power event stream (heartbeat / Last-Event-ID)."""

import asyncio
import json
from datetime import datetime
from unittest.mock import AsyncMock, patch

import app.crud as crud
import app.services.stream as stream

FAKE_POWER = {"timestamp": datetime(2026, 6, 17, 12, 0, 0), "data": {"total_power_watts": 100.0}}


def _take(gen, n):
    async def run():
        items = []
        for _ in range(n):
            items.append(await gen.__anext__())
        await gen.aclose()
        return items

    return asyncio.run(run())


def test_sse_heartbeat_format():
    hb = stream._sse_heartbeat()
    assert hb.startswith("event: heartbeat\ndata: ") and hb.endswith("\n\n")
    assert "id:" not in hb  # heartbeats must not advance Last-Event-ID
    assert "timestamp" in json.loads(hb.split("data: ", 1)[1])


def test_sse_event_carries_id():
    e = stream._sse_event("power_spike", {"x": 1}, 7)
    assert e.startswith("id: 7\nevent: power_spike\ndata: ")
    assert json.loads(e.split("data: ", 1)[1]) == {"x": 1}


def test_fresh_connection_starts_with_heartbeat():
    with patch.object(crud, "get_unified_power", new=AsyncMock(return_value=FAKE_POWER)), \
         patch.object(stream.asyncio, "sleep", new=AsyncMock(return_value=None)):
        items = _take(stream.power_events_generator(None, None, None), 3)
    assert items[0].startswith("event: heartbeat") and "id:" not in items[0]
    assert all(i.startswith("event: heartbeat") for i in items)  # idle stream -> heartbeats


def test_reconnect_emits_snapshot_with_continued_id():
    with patch.object(crud, "get_unified_power", new=AsyncMock(return_value=FAKE_POWER)), \
         patch.object(stream.asyncio, "sleep", new=AsyncMock(return_value=None)):
        first = _take(stream.power_events_generator(None, None, None, last_event_id="5"), 1)[0]
    assert first.startswith("id: 6\nevent: snapshot")  # replay impossible -> snapshot, id continues


def test_threshold_event_carries_id():
    with patch.object(crud, "get_unified_power", new=AsyncMock(return_value=FAKE_POWER)), \
         patch.object(stream.asyncio, "sleep", new=AsyncMock(return_value=None)):
        # threshold 50 < 100 -> threshold_exceeded; reconnect so snapshot=id6, event=id7
        items = _take(stream.power_events_generator(None, None, 50.0, last_event_id="5"), 2)
    assert items[1].startswith("id: 7\nevent: threshold_exceeded")
