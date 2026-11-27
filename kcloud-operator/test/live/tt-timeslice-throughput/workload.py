# ============================================================
# workload.py: Tenstorrent Blackhole 에서 ttnn matmul 을 반복해 초당 처리량 측정
# 상세: PHASE·DURATION·DEVICE_ID·MATMUL_DIM·START_AT env 로 ttnn.open_device 를 열어 matmul 을
#       반복하고 RESULT/metric 줄을 낸다(배리어·측정 계약은 README 참고).
# 생성일: 2026-09-22 | 수정일: 2026-10-11
# ============================================================
import os
import sys
import json
import time
import datetime


def iso(ts):
    return datetime.datetime.utcfromtimestamp(ts).isoformat() + "Z"


def emit(obj):
    print("RESULT " + json.dumps(obj), flush=True)


phase = os.environ.get("PHASE", "unknown")
duration = float(os.environ.get("DURATION", "60"))
dev_id = int(os.environ.get("DEVICE_ID", "0"))
dim = int(os.environ.get("MATMUL_DIM", "2048"))
warmup = int(os.environ.get("WARMUP_ITERS", "3"))

proc_start = time.time()
device = None
try:
    import torch
    import ttnn

    device = ttnn.open_device(device_id=dev_id)
    ta = torch.randn(dim, dim, dtype=torch.bfloat16)
    tb = torch.randn(dim, dim, dtype=torch.bfloat16)
    a = ttnn.from_torch(ta, layout=ttnn.TILE_LAYOUT, device=device, dtype=ttnn.bfloat16)
    b = ttnn.from_torch(tb, layout=ttnn.TILE_LAYOUT, device=device, dtype=ttnn.bfloat16)

    # 워밍업: 첫 matmul 은 커널 컴파일로 느리다 — 타이머 밖에서 소진해 정상 상태 처리량만 잰다.
    for _ in range(warmup):
        c = ttnn.matmul(a, b)
        ttnn.synchronize_device(device)
        ttnn.deallocate(c)

    # 초기화 관측: open_device·워밍업까지 걸린 시간과 START_AT 까지 남은 대기를 plain 로그로 남긴다
    # (RESULT JSON 은 그대로). run.sh 가 파드별로 이 줄을 grep 해 summary 에 두 init 시간을 싣는다 —
    # 동시 open 시 init 이 직렬화되는지도 이 값으로 드러난다.
    init_seconds = time.time() - proc_start
    start_at = os.environ.get("START_AT", "").strip()
    target = float(start_at) if start_at else 0.0
    wait_seconds = (target - time.time()) if start_at else 0.0
    print("INIT phase=%s init_seconds=%.2f wait_seconds=%.2f" % (phase, init_seconds, wait_seconds), flush=True)

    # 시작 배리어: START_AT(epoch 초)이 있으면 그 시각까지 대기했다가 타이머를 시작해 co-run 두 파드의
    # 측정 창을 정렬한다(겹침 보장). START_AT 이 5초 넘게 과거면 배리어가 짧거나 초기화가 느린 것이므로,
    # 어긋난 창을 조용히 재지 말고 error 로 실패한다(exit 1). solo 는 START_AT 없이 즉시 시작한다.
    if start_at:
        if time.time() - target > 5.0:
            emit({"phase": phase, "error": "START_AT %.3f past by %.1fs (barrier too short/init too slow)" % (target, time.time() - target)})
            ttnn.close_device(device)
            sys.exit(1)
        delay = target - time.time()
        if delay > 0:
            time.sleep(delay)

    start = time.time()
    end_target = start + duration
    iters = 0
    while time.time() < end_target:
        c = ttnn.matmul(a, b)
        ttnn.synchronize_device(device)
        ttnn.deallocate(c)
        iters += 1
    end = time.time()

    seconds = end - start
    rate = (iters / seconds) if seconds > 0 else 0.0
    emit({
        "phase": phase,
        "iters": iters,
        "seconds": round(seconds, 3),
        "iters_per_sec": round(rate, 4),
        "dim": dim,
        "start": iso(start),
        "end": iso(end),
    })
    print("metric=matmul_per_s value=%.4f" % rate, flush=True)
    ttnn.close_device(device)
    sys.exit(0)
except Exception as e:  # noqa: BLE001 — 어떤 실패든 RESULT error 로 남기고 분류는 run.sh 가 한다
    emit({"phase": phase, "error": repr(e)})
    try:
        if device is not None:
            import ttnn
            ttnn.close_device(device)
    except Exception:
        pass
    sys.exit(1)
