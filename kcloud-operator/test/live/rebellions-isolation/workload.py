# ============================================================
# workload.py: Rebellions ATOM 네 축(연산·용량·대역폭·장애) 격리 실측용 워크로드
# 상세: PHASE·DURATION·DEVICE_ID·OP·MATMUL_DIM·BW_DIM·CHAIN_K·START_AT·BAD_SHAPE env 로 축을
#       고르고 rebel 런타임으로 장치를 열어 반복 실행, RESULT/INIT/PHASE_READY 줄로 알린다.
# 생성일: 2026-09-28 | 수정일: 2026-10-11
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
op = os.environ.get("OP", "matmul")
dim = int(os.environ.get("MATMUL_DIM", "2048"))
bw_dim = int(os.environ.get("BW_DIM", str(dim)))
chain_k = int(os.environ.get("CHAIN_K", "1"))
warmup = int(os.environ.get("WARMUP_ITERS", "3"))
bad_shape = os.environ.get("BAD_SHAPE", "") == "1"

proc_start = time.time()
device_open_seconds = 0.0
init_seconds = 0.0
try:
    import numpy as np
    import torch
    import torch.nn as nn
    import rebel

    if op == "scale":
        # 대역폭 축: 연산은 원소별 곱 하나뿐 — 처리시간을 호스트↔장치 전송이 지배하게 한다.
        run_dim = bw_dim

        class Scale(nn.Module):
            def forward(self, x):
                return x * 2.0

        model = Scale().eval()
    elif op == "chain":
        # 용량 축: Linear+ReLU 를 CHAIN_K 번 이어 붙여 가중치 메모리 점유를 키운다.
        run_dim = dim

        class Chain(nn.Module):
            def __init__(self, k, d):
                super().__init__()
                layers = []
                for _ in range(k):
                    layers += [nn.Linear(d, d, bias=False), nn.ReLU()]
                self.net = nn.Sequential(*layers)

            def forward(self, x):
                return self.net(x)

        model = Chain(chain_k, dim).eval()
    else:
        # 연산·장애 축: 단순 행렬곱 부하(rebellions-timeslice 와 동일).
        run_dim = dim

        class MatMul(nn.Module):
            def __init__(self, k):
                super().__init__()
                self.lin = nn.Linear(k, k, bias=False)

            def forward(self, x):
                return self.lin(x)

        model = MatMul(dim).eval()

    compiled = rebel.compile_from_torch(
        model,
        input_info=[("x", [run_dim, run_dim], "float32")],
        npu=rebel.get_npu_name(),
    )

    # create_runtime 이 장치를 연다 — 이 소요를 따로 잰다(동시 open 직렬화가 드러나는 지점).
    open_t0 = time.time()
    rt = compiled.create_runtime(tensor_type="np", device=dev_id)
    device_open_seconds = time.time() - open_t0

    x = np.random.randn(run_dim, run_dim).astype(np.float32)

    # 워밍업: 첫 실행은 장치 로드로 느리다 — 타이머 밖에서 소진해 정상 상태 처리량만 잰다.
    for _ in range(warmup):
        rt(x)

    init_seconds = time.time() - proc_start
    start_at = os.environ.get("START_AT", "").strip()
    target = float(start_at) if start_at else 0.0
    wait_seconds = (target - time.time()) if start_at else 0.0
    print(
        "INIT phase=%s op=%s dim=%d chainK=%d init_seconds=%.2f device_open_seconds=%.2f wait_seconds=%.2f"
        % (phase, op, run_dim, chain_k, init_seconds, device_open_seconds, wait_seconds),
        flush=True,
    )

    # 시작 배리어: START_AT(epoch 초)이 있으면 그 시각까지 대기했다가 타이머를 시작해 co-run
    # 프로세스들의 측정 창을 정렬한다. 5초 넘게 과거면 어긋난 창을 조용히 재지 말고 error 로
    # 실패한다(rebellions-timeslice 와 같은 계약).
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

    # 장애 축 "잘못된 입력" 단계: 정상 반복 대신 형상이 다른 입력으로 한 번 호출해 실패를
    # 강제한다. 예외 없이 성공하면(형상 불일치가 안 걸리면) 그 사실 자체를 error 로 남긴다 —
    # 성공을 조용히 실패로 둔갑시키지 않는다.
    if bad_shape:
        try:
            rt(np.random.randn(run_dim + 7, run_dim + 7).astype(np.float32))
            raise RuntimeError("BAD_SHAPE 호출이 예외 없이 성공함 — 형상 불일치가 걸리지 않음")
        except Exception as e:  # noqa: BLE001 — 어떤 실패든 RESULT 로 남기고 분류는 run.sh 가 한다
            emit(
                {
                    "phase": phase,
                    "faultInjection": "bad_shape",
                    "error": repr(e),
                    "device_open_seconds": round(device_open_seconds, 3),
                    "init_seconds": round(init_seconds, 3),
                }
            )
            sys.exit(1)

    print("PHASE_READY %s" % phase, flush=True)

    start = time.time()
    end_target = start + duration
    iters = 0
    while time.time() < end_target:
        rt(x)
        iters += 1
    end = time.time()

    seconds = end - start
    rate = (iters / seconds) if seconds > 0 else 0.0
    bytes_per_iter = 2 * run_dim * run_dim * 4  # 입력+출력 왕복 바이트(대역폭 축 근사)
    emit(
        {
            "phase": phase,
            "op": op,
            "iters": iters,
            "seconds": round(seconds, 3),
            "iters_per_sec": round(rate, 4),
            "bytes_per_sec": round(bytes_per_iter * rate, 1),
            "dim": run_dim,
            "chainK": chain_k if op == "chain" else None,
            "device_open_seconds": round(device_open_seconds, 3),
            "init_seconds": round(init_seconds, 3),
            "start": iso(start),
            "end": iso(end),
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
