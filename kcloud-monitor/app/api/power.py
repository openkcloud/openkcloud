from fastapi import APIRouter, Depends, HTTPException, Query
from fastapi.responses import Response, JSONResponse
from datetime import datetime
from typing import Optional

# Authentication handled at router level in main.py
from app.models.queries import GPUQueryParams, TimeSeriesQueryParams, ExportQueryParams
from app.models.responses import GPUPowerResponse, TimeSeriesResponse
from app.services import cache_service
from app import crud

router = APIRouter()

@router.get("/power/gpu",
            response_model=GPUPowerResponse)
async def get_gpu_power(
    params: GPUQueryParams = Depends(),
    include_dcgm: bool = Query(False, description="Include DCGM data integration (hybrid mode)")
):
    cache_key = f"gpu_power_{params.period.value}_{params.instance or 'all'}_{params.cluster or 'default'}_{params.node or 'all'}_dcgm_{include_dcgm}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data

    # Choose data function based on DCGM integration option
    if include_dcgm:
        data = await crud.get_enhanced_gpu_power_data(params)
    else:
        data = await crud.get_gpu_power_data(params)

    await cache_service.set(cache_key, data, ttl=30) # Cache for 30 seconds
    return data

@router.get("/power/gpu/{instance}",
            response_model=GPUPowerResponse)
async def get_gpu_power_for_instance(
    instance: str,
    params: GPUQueryParams = Depends(),
    include_dcgm: bool = Query(False, description="Include DCGM data integration (hybrid mode)")
):
    params.instance = instance # Override instance from path
    # The logic is the same as the general endpoint, just with a forced instance
    return await get_gpu_power(params, include_dcgm)

@router.get("/power/timeseries", 
            response_model=TimeSeriesResponse)
async def get_power_timeseries(params: TimeSeriesQueryParams = Depends()):
    cache_key = f"timeseries_{params.period.value}_{params.step}_{params.instance or 'all'}"
    cached_data = await cache_service.get(cache_key)
    if cached_data:
        return cached_data
        
    data = await crud.get_timeseries_data(params)
    await cache_service.set(cache_key, data, ttl=300) # Cache for 5 minutes
    return data

@router.get("/power/export")
async def export_power_data(params: ExportQueryParams = Depends()):
    gpu_params = GPUQueryParams(period=params.period, instance=params.instance)
    data = await crud.get_gpu_power_data(gpu_params)
    
    try:
        export_data = crud.format_data_for_export(data, params.format.value)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))

    media_type = "application/json" if params.format == "json" else "text/csv"
    filename = f"power_export_{datetime.utcnow().strftime('%Y%m%d%H%M%S')}.{params.format.value}"
    
    return Response(content=export_data, 
                    media_type=media_type, 
                    headers={"Content-Disposition": f"attachment; filename={filename}"})
