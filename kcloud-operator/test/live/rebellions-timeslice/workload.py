# ============================================================
# workload.py: Rebellions ATOM 에서 컴파일한 행렬곱 모델을 반복 실행해 초당 처리량 측정
# 상세: PHASE·DURATION·DEVICE_ID·MATMUL_DIM·START_AT·RBLN_COMPILED_PATH env 로 동작을 고르고
#       RESULT/metric 줄을 stdout 에 낸다(필드·배리어·사전 컴파일 계약은 README 참고).
# 생성일: 2026-09-25 | 수정일: 2026-10-11
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
compiled_path = os.environ.get("RBLN_COMPILED_PATH", "").strip()

proc_start = time.time()
device_open_seconds = 0.0
init_seconds = 0.0
compile_seconds = 0.0
compiled_from = "compile"
try:
    import numpy as np
    import torch
    import torch.nn as nn
    import rebel

    # 단순 행렬곱 부하: Linear(dim, dim) 에 [dim, dim] 입력을 흘려 dim^3 MAC 을 반복한다.
    class MatMul(nn.Module):
        def __init__(self, k):
            super().__init__()
            self.lin = nn.Linear(k, k, bias=False)

        def forward(self, x):
            return self.lin(x)

    if phase == "compile-only":
        # 호스트 사전 컴파일 전용: 컴파일해 RBLN_COMPILED_PATH 에 저장만 하고 끝난다(장치를
        # 열지 않는다). run.sh 의 precompile_host 가 파드 실행 전에 이 phase 로 한 번 호출한다.
        if not compiled_path:
            raise RuntimeError("PHASE=compile-only 는 RBLN_COMPILED_PATH 가 필요하다")
        npu = rebel.get_npu_name() or os.environ.get("RBLN_NPU_NAME")
        model = MatMul(dim).eval()
        t0 = time.time()
        compiled = rebel.compile_from_torch(
            model, input_info=[("x", [dim, dim], "float32")], npu=npu
        )
        compiled.save(compiled_path)
        print(
            "COMPILE_ONLY_OK npu=%s path=%s seconds=%.2f"
            % (npu, compiled_path, time.time() - t0),
            flush=True,
        )
        sys.exit(0)

    if compiled_path:
        # 파드 적재 경로: 호스트가 사전 컴파일한 파일을 그대로 연다(파드 안 compile_from_torch
        # 회피 — 벤더가 실패 원인을 봉인해 둬 고칠 수 없다).
        compiled = rebel.RBLNCompiledModel(compiled_path)
        compiled_from = "file"
    else:
        model = MatMul(dim).eval()
        npu = rebel.get_npu_name() or os.environ.get("RBLN_NPU_NAME")
        compile_t0 = time.time()
        compiled = rebel.compile_from_torch(
            model, input_info=[("x", [dim, dim], "float32")], npu=npu
        )
        compile_seconds = time.time() - compile_t0

    # create_runtime 이 장치를 연다 — 이 소요를 따로 잰다(동시 open 직렬화가 드러나는 지점).
    open_t0 = time.time()
    rt = compiled.create_runtime(tensor_type="np", device=dev_id)
    device_open_seconds = time.time() - open_t0

    x = np.random.randn(dim, dim).astype(np.float32)

    # 워밍업: 첫 실행은 장치 로드로 느리다 — 타이머 밖에서 소진해 정상 상태 처리량만 잰다.
    for _ in range(warmup):
        rt(x)

    # 초기화 관측: 프로세스 시작→워밍업 종료(init_seconds)와 장치 open(device_open_seconds)을
    # plain 로그로 남긴다. run.sh 가 파드별로 이 줄을 grep 해 summary 에 싣는다 — 동시 open 시
    # init 이 직렬화되는지도 이 값으로 드러난다.
    init_seconds = time.time() - proc_start
    start_at = os.environ.get("START_AT", "").strip()
    target = float(start_at) if start_at else 0.0
    wait_seconds = (target - time.time()) if start_at else 0.0
    print(
        "INIT phase=%s init_seconds=%.2f device_open_seconds=%.2f wait_seconds=%.2f"
        % (phase, init_seconds, device_open_seconds, wait_seconds),
        flush=True,
    )

    # 시작 배리어: START_AT(epoch 초)이 있으면 그 시각까지 대기했다가 타이머를 시작해 co-run 두
    # 프로세스의 측정 창을 정렬한다(겹침 보장). START_AT 이 5초 넘게 과거면 배리어가 짧거나
    # 초기화가 느린 것이므로, 어긋난 창을 조용히 재지 말고 error 로 실패한다(exit 1). solo 는
    # START_AT 없이 즉시 시작한다.
    if start_at:
        if time.time() - target > 5.0:
            emit(
                {
                    "phase": phase,
                    "error": "START_AT %.3f past by %.1fs (barrier too short/init too slow)"
                    % (target, time.time() - target),
                    "device_open_seconds": round(device_open_seconds, 3),
                    "init_seconds": round(init_seconds, 3),
                }
            )
            sys.exit(1)
        delay = target - time.time()
        if delay > 0:
            time.sleep(delay)

    start = time.time()
    end_target = start + duration
    iters = 0
    while time.time() < end_target:
        rt(x)
        iters += 1
    end = time.time()

    seconds = end - start
    rate = (iters / seconds) if seconds > 0 else 0.0
    emit(
        {
            "phase": phase,
            "iters": iters,
            "seconds": round(seconds, 3),
            "iters_per_sec": round(rate, 4),
            "dim": dim,
            "device_open_seconds": round(device_open_seconds, 3),
            "init_seconds": round(init_seconds, 3),
            "start": iso(start),
            "end": iso(end),
            "compile_seconds": round(compile_seconds, 3),
            "compiled_from": compiled_from,
        }
    )
    print("metric=matmul_per_s value=%.4f" % rate, flush=True)
    sys.exit(0)
except Exception as e:  # noqa: BLE001 — 어떤 실패든 RESULT error 로 남기고 분류는 run.sh 가 한다
    emit(
        {
            "phase": phase,
            "error": repr(e),
            "device_open_seconds": round(device_open_seconds, 3),
            "init_seconds": round(init_seconds, 3),
        }
    )
    sys.exit(1)
