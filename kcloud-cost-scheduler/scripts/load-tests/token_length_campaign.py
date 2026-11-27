"""두 서버 공통 토큰 길이 단회 실험. 역할 SSE와 첫 콘텐츠를 별도 기록.

새 이름의 시험 컨테이너만 생성·종료하며 기존 워크로드·Catalog는 수정하지 않는다.
장치 전력과 서버 입력 전력을 합산하지 않는다. 원본 스트림을 남겨 재검산 가능.
"""
from __future__ import annotations

import argparse
import bisect
import csv
import hashlib
import json
import os
import re
import signal
import socket
import subprocess
import threading
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from statistics import mean
from zoneinfo import ZoneInfo

CELLS = [(83, 128), (512, 128), (2048, 128), (83, 256), (83, 512)]
GPU_IMAGE = 'sha256:0c19f07f0c8601d5b77e5a95e188115c17e5d9ddab105462be4951c92f4ebdbb'
NPU_IMAGE = 'sha256:814d9a2f28d254d6c5914fa58ddb8be3f0176d58e753a5dacfc05ab2723c865a'
GPU_MODEL = 'meta-llama/Llama-3.1-8B-Instruct'
NPU_MODEL = 'furiosa-ai/Llama-3.1-8B-Instruct'
STOP = threading.Event()


def utc():
    return datetime.now(timezone.utc).isoformat()


def write_json(path, obj):
    path.write_text(json.dumps(obj, ensure_ascii=False, indent=2), encoding='utf-8')


def command(args, timeout=20, check=True):
    r = subprocess.run(args, text=True, capture_output=True, timeout=timeout)
    if check and r.returncode:
        raise RuntimeError(f'{args[:3]}: {r.stderr[-1500:]}')
    return r.stdout.strip()


def get_json(url, timeout=5):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return json.load(r)


def p99(xs):
    if not xs:
        return None
    xs = sorted(xs)
    return xs[min(int(.99 * len(xs)), len(xs) - 1)]


class StreamState:
    def __init__(self):
        self.first_sse_ms = None
        self.content_ms = []
        self.empty_content_events = 0
        self.usage = None
        self.finish_reason = None
        self.done = False
        self.text = []

    def consume(self, data, elapsed_ms):
        if data == '[DONE]':
            self.done = True
            return
        if self.first_sse_ms is None:
            self.first_sse_ms = elapsed_ms
        obj = json.loads(data)
        if obj.get('error'):
            raise RuntimeError(str(obj['error']))
        if obj.get('usage') is not None:
            self.usage = obj['usage']
        choices = obj.get('choices') or []
        if not choices:
            return
        choice = choices[0]
        if choice.get('finish_reason') is not None:
            self.finish_reason = choice['finish_reason']
        text = (choice.get('delta') or {}).get('content')
        if isinstance(text, str) and text != '':
            self.content_ms.append(elapsed_ms)
            self.text.append(text)
        else:
            self.empty_content_events += 1


def request(url, model, content, cap, stream_path=None, request_id='probe'):
    payload = {'model': model, 'messages': [{'role': 'user', 'content': content}],
               'max_tokens': cap, 'temperature': 0, 'stream': True,
               'stream_options': {'include_usage': True}}
    req = urllib.request.Request(url + '/v1/chat/completions',
                                 data=json.dumps(payload).encode(),
                                 headers={'Content-Type': 'application/json'})
    state = StreamState()
    wall = time.time(); start_ns = time.perf_counter_ns(); error = None
    raw_file = stream_path.open('w', encoding='utf-8') if stream_path else None
    try:
        with urllib.request.urlopen(req, timeout=180) as response:
            for raw in response:
                elapsed = (time.perf_counter_ns() - start_ns) / 1e6
                line = raw.decode('utf-8').rstrip('\r\n')
                if raw_file:
                    raw_file.write(json.dumps({'elapsed_ms': elapsed, 'raw': line}, ensure_ascii=False) + '\n')
                if line.startswith('data:'):
                    state.consume(line[5:].strip(), elapsed)
                    if state.done:
                        break
        if not state.done or not state.content_ms or not state.usage or state.finish_reason is None:
            raise RuntimeError('incomplete stream/content/usage/finish_reason')
        if int(state.usage.get('prompt_tokens', 0)) <= 0 or int(state.usage.get('completion_tokens', 0)) <= 0:
            raise RuntimeError('invalid usage token counts')
    except Exception as exc:
        error = str(exc)
    finally:
        if raw_file:
            raw_file.close()
    total_ms = (time.perf_counter_ns() - start_ns) / 1e6
    usage = state.usage or {}
    out = int(usage.get('completion_tokens') or 0)
    ttft = state.content_ms[0] if state.content_ms else None
    tpot = ((state.content_ms[-1] - ttft) / (out - 1)
            if ttft is not None and out > 1 else None)
    return {'request_id': request_id, 'started_epoch': wall, 'ended_epoch': time.time(),
            'started_perf_counter_ns': start_ns, 'first_sse_ms': state.first_sse_ms,
            'ttft_ms': ttft, 'last_content_ms': state.content_ms[-1] if state.content_ms else None,
            'total_ms': total_ms, 'request_mean_tpot_ms': tpot,
            'content_chunk_elapsed_ms': state.content_ms,
            'content_chunks_are_not_token_count': True,
            'empty_content_events': state.empty_content_events,
            'prompt_tokens': int(usage.get('prompt_tokens') or 0), 'completion_tokens': out,
            'finish_reason': state.finish_reason, 'stream_done': state.done,
            'output_text': ''.join(state.text), 'error': error}


class Samplers:
    def __init__(self, directory, netio, endpoint=None):
        self.directory = directory; self.netio = netio; self.endpoint = endpoint
        self.stop_event = threading.Event(); self.threads = []; self.samples = {}

    def read(self, channel):
        if channel == 'server':
            d = get_json(self.netio, 2.5)
            outlets = {int(o['ID']): o for o in d['Outputs']}
            return {'watts': float(outlets[1]['Load']) + float(outlets[2]['Load']),
                    'raw': d}
        if channel == 'gpu':
            text = command(['nvidia-smi', '--query-gpu=power.draw,temperature.gpu,utilization.gpu,memory.used',
                            '--format=csv,noheader,nounits'], timeout=3)
            parts = text.splitlines()[0].split(',')
            return dict(zip(['watts', 'temperature_C', 'utilization_pct', 'memory_MiB'], map(float, parts)))
        if channel == 'npu':
            text = command(['furiosa-smi', 'info'], timeout=3)
            line = next((s for s in text.splitlines() if 'rngd' in s and re.search(r'[\d.]+\s*W', s)), '')
            m = re.search(r'([\d.]+)\s*W', line)
            if not m:
                raise RuntimeError('NPU power missing')
            return {'watts': float(m.group(1)), 'raw': text}
        if channel == 'runtime':
            with urllib.request.urlopen(self.endpoint + '/metrics', timeout=3) as r:
                return {'prometheus': r.read().decode()}
        mem = Path('/proc/meminfo').read_text()
        result = {'loadavg': Path('/proc/loadavg').read_text().strip(),
                  'meminfo': mem, 'cpu_stat': Path('/proc/stat').read_text()}
        path = Path('/sys/bus/acpi/drivers/power_meter/ACPI000D:00/power1_average')
        if path.exists():
            result['acpi_raw_microW_unconfirmed_scope'] = path.read_text().strip()
        return result

    def worker(self, channel):
        self.samples[channel] = []
        with (self.directory / f'{channel}.jsonl').open('w', encoding='utf-8') as f:
            while not self.stop_event.is_set():
                started = time.time()
                try:
                    row = {'ok': True, **self.read(channel)}
                except Exception as e:
                    row = {'ok': False, 'error': str(e)}
                ended = time.time()
                row.update(started_epoch=started, ended_epoch=ended, epoch=(started + ended) / 2)
                if row.get('ok') and 'watts' in row:
                    self.samples[channel].append((row['epoch'], row['watts']))
                f.write(json.dumps(row, ensure_ascii=False) + '\n'); f.flush()
                self.stop_event.wait(max(0, 1 - (time.time() - started)))

    def start(self):
        self.directory.mkdir(parents=True, exist_ok=False)
        channels = ['server', 'gpu', 'npu', 'host'] + (['runtime'] if self.endpoint else [])
        for channel in channels:
            t = threading.Thread(target=self.worker, args=(channel,), daemon=True)
            self.threads.append(t); t.start()

    def close(self):
        self.stop_event.set()
        for t in self.threads:
            t.join(5)


def integrate(samples, start, end, idle=0):
    gross = net = coverage = 0.0; gaps = 0
    for (ta, pa), (tb, pb) in zip(samples, samples[1:]):
        left, right = max(start, ta), min(end, tb)
        if right <= left:
            continue
        if tb - ta > 5:
            gaps += 1; continue
        pleft = pa + (pb - pa) * (left - ta) / (tb - ta)
        pright = pa + (pb - pa) * (right - ta) / (tb - ta)
        dt = right - left
        gross += (pleft + pright) / 2 * dt
        net += ((pleft + pright) / 2 - idle) * dt
        coverage += dt
    return {'gross_J': gross, 'net_J': net, 'covered_s': coverage,
            'window_s': end - start, 'coverage': coverage / (end - start), 'gaps': gaps,
            'eligible': coverage / (end - start) >= .99, 'idle_W': idle,
            'gross_definition': 'trapezoid in request workload window; gaps>5s excluded',
            'net_definition': 'gross - engine-off idle over covered time; not per-workload attribution'}


class Campaign:
    def __init__(self, a):
        self.a = a; self.out = Path(a.output); self.cid = None; self.endpoint = None; self.created = False
        self.manifest = json.loads(Path(a.manifest).read_text())
        self.status = {'started_utc': utc(), 'host': socket.gethostname(), 'state': 'preflight',
                       'completed': [], 'single_launch_only': True, 'new_experiment': True}
        self.baseline_containers = set(command(['docker', 'ps', '-q']).splitlines())

    def update(self, **kwargs):
        self.status.update(kwargs, updated_utc=utc()); write_json(self.out / 'status.json', self.status)
        print(json.dumps(kwargs, ensure_ascii=False), flush=True)

    def allowed(self):
        now = datetime.now(ZoneInfo('Asia/Seoul'))
        if STOP.is_set() or (self.out / 'STOP').exists() or now.hour * 60 + now.minute >= 20 * 60 + 55:
            raise RuntimeError('stop requested or 20:55 KST hard stop reached')
        extra = set(command(['docker', 'ps', '-q']).splitlines()) - self.baseline_containers
        if self.cid:
            extra = {s for s in extra if not self.cid.startswith(s)}
        if extra:
            raise RuntimeError('new foreign container appeared: refuse interference')
        if self.cid:
            allowed_pids = set(command(['docker', 'top', self.cid, '-eo', 'pid']).split())
            gpu_apps = command(['nvidia-smi', '--query-compute-apps=pid,process_name', '--format=csv,noheader'])
            for line in gpu_apps.splitlines():
                pid = line.split(',')[0].strip()
                if pid.isdigit() and 'nvidia-cuda-mps' not in line and pid not in allowed_pids:
                    raise RuntimeError(f'foreign GPU process {pid}; refuse interference')

    def empty(self):
        self.allowed()
        values = command(['nvidia-smi', '--query-gpu=memory.used,utilization.gpu',
                          '--format=csv,noheader,nounits']).split(',')
        if float(values[0]) > 800 or float(values[1]) > 5:
            raise RuntimeError('GPU occupied; no foreign job will be stopped')
        apps = command(['furiosa-smi', 'ps'])
        for line in apps.splitlines():
            if re.match(r'\s*\|\s*\d+\s*\|', line):
                raise RuntimeError('NPU occupied; no foreign job will be stopped')

    def start(self, device):
        self.empty()
        image = GPU_IMAGE if device == 'gpu' else NPU_IMAGE
        name = f'token-{self.a.host_tag}-{device}-{int(time.time())}'
        args = ['docker', 'run', '-d', '--name', name, '--label', 'kcload.token-length-campaign=1', '--restart=no']
        if device == 'gpu':
            args += ['--gpus', 'all', '-p', '8000:8000']
        else:
            args += ['--device', '/dev/rngd', '--security-opt', 'seccomp=unconfined', '-p', '8001:8000']
        args += ['-v', f'{self.a.hf_cache}:/root/.cache/huggingface', image]
        if device == 'gpu':
            args += ['--model', GPU_MODEL, '--revision', '0e9e39f249a16976918f6564b8830bc894c89659',
                     '--dtype', 'auto', '--gpu-memory-utilization', '0.90', '--max-model-len', '4096',
                     '--max-num-seqs', '64', '--no-enable-prefix-caching', '--disable-log-requests']
        else:
            args += ['serve', NPU_MODEL, '--host', '0.0.0.0', '--port', '8000']
        write_json(self.out / f'{device}_launch.json', {'command': args, 'image_id': image,
                   'cache_refs': {m: (Path(self.a.hf_cache) / 'hub' / f'models--{m.replace("/", "--")}' / 'refs/main').read_text().strip()
                                  for m in [GPU_MODEL, NPU_MODEL]}})
        self.cid = command(args, timeout=90)
        self.endpoint = 'http://127.0.0.1:' + ('8000' if device == 'gpu' else '8001')
        self.update(state='engine_starting', device=device, container_id=self.cid)
        deadline = time.time() + 1200
        while time.time() < deadline:
            self.allowed()
            state = command(['docker', 'inspect', self.cid, '--format', '{{.State.Status}}'])
            if state != 'running':
                raise RuntimeError(f'engine exited: {state}')
            try:
                model = get_json(self.endpoint + '/v1/models', 3)['data'][0]['id']
                probe = request(self.endpoint, model, self.manifest['rows'][0]['in83'], 8)
                write_json(self.out / f'{device}_probe.json', probe)
                if not probe['error']:
                    return model
            except Exception:
                pass
            time.sleep(5)
        raise RuntimeError('engine chat readiness timeout')

    def cleanup(self):
        if self.cid:
            try:
                for suffix, args in [('logs.txt', ['docker', 'logs', self.cid]),
                                     ('inspect.json', ['docker', 'inspect', self.cid])]:
                    try:
                        r = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
                        (self.out / f'{self.status.get("device", "engine")}_{suffix}').write_text(r.stdout, encoding='utf-8')
                    except Exception as exc:
                        print(f'capture warning: {exc}', flush=True)
            finally:
                command(['docker', 'stop', '-t', '30', self.cid], timeout=45, check=False)
                self.cid = None

    def pause(self, seconds):
        for _ in range(seconds):
            self.allowed(); time.sleep(1)

    def cell(self, device, model, inp, cap, idle):
        self.allowed()
        cell = self.out / device / f'in{inp}_outcap{cap}_c1'
        cell.mkdir(parents=True, exist_ok=False); (cell / 'streams').mkdir()
        prompts = self.manifest['rows']
        warm = []
        for i in range(30):
            self.allowed()
            r = request(self.endpoint, model, prompts[i][f'in{inp}'], cap,
                        cell / 'streams' / f'warmup-{i:03}.jsonl', f'warmup-{i}')
            warm.append(r)
            if r['error'] or r['prompt_tokens'] != inp:
                write_json(cell / 'warmup_failure.json', r)
                raise RuntimeError(f'warmup error or token mismatch: target={inp}, actual={r["prompt_tokens"]}, error={r["error"]}')
        write_json(cell / 'warmup.json', warm)
        logger = Samplers(cell / 'samples', self.a.netio, self.endpoint)
        logger.start(); time.sleep(3)
        results = []; start = time.time(); self.update(state='running', device=device, cell=cell.name, progress=0)
        try:
            with (cell / 'requests.jsonl').open('w', encoding='utf-8') as f:
                for i, p in enumerate(prompts[30:630]):
                    if i % 10 == 0:
                        self.allowed()
                    r = request(self.endpoint, model, p[f'in{inp}'], cap,
                                cell / 'streams' / f'{i:04}.jsonl', f'{i:04}')
                    results.append(r); f.write(json.dumps(r, ensure_ascii=False) + '\n'); f.flush()
                    if r['error'] or r['prompt_tokens'] != inp:
                        raise RuntimeError(f'request error/token mismatch at {device}/{cell.name}/{i}: {r["error"]}')
                    if (i + 1) % 25 == 0:
                        self.update(progress=i + 1)
            end = time.time(); time.sleep(3)
        finally:
            logger.close()
        n = len(results); out_tokens = sum(r['completion_tokens'] for r in results)
        ttfts = [r['ttft_ms'] for r in results]
        performance = {'host': self.a.host_tag, 'device': device, 'model': model,
                       'input_tokens_target': inp, 'output_cap': cap, 'concurrency': 1,
                       'requests': n, 'errors': 0, 'duration_s': end - start,
                       'started_epoch': start, 'ended_epoch': end,
                       'qps': n / (end - start), 'ttft_p99_ms': p99(ttfts),
                       'first_sse_p99_ms': p99([r['first_sse_ms'] for r in results]),
                       'total_p99_ms': p99([r['total_ms'] for r in results]),
                       'request_mean_tpot_p99_ms': p99([r['request_mean_tpot_ms'] for r in results if r['request_mean_tpot_ms'] is not None]),
                       'ttft_budget_ms': 2000, 'ttft_slo_pass': p99(ttfts) <= 2000,
                       'timely_content_requests': sum(t <= 2000 for t in ttfts),
                       'input_tokens_sum': sum(r['prompt_tokens'] for r in results),
                       'output_tokens_sum': out_tokens, 'output_tokens_mean': out_tokens / n,
                       'output_tokens_min': min(r['completion_tokens'] for r in results),
                       'output_tokens_max': max(r['completion_tokens'] for r in results),
                       'finish_reason_counts': {reason: sum(r['finish_reason'] == reason for r in results)
                                                for reason in {r['finish_reason'] for r in results}},
                       'power': {}}
        for source in ['server', 'gpu', 'npu']:
            energy = integrate(logger.samples.get(source, []), start, end, idle[source])
            energy.update(gross_J_request=energy['gross_J'] / n,
                          gross_J_output_token=energy['gross_J'] / out_tokens)
            performance['power'][source] = energy
        write_json(cell / 'performance.json', performance)
        self.status['completed'].append({'device': device, 'cell': cell.name,
                                         'ttft_p99_ms': performance['ttft_p99_ms'],
                                         'total_p99_ms': performance['total_p99_ms'],
                                         'output_tokens_mean': performance['output_tokens_mean']})
        self.update(state='cell_complete', progress=n)

    def run(self):
        self.out.mkdir(parents=True, exist_ok=False)
        self.created = True
        self.update(state='preflight')
        write_json(self.out / 'protocol.json', {'date_kst': datetime.now(ZoneInfo('Asia/Seoul')).date().isoformat(), 'cells': CELLS,
                   'scored_requests_per_cell': 600, 'warmup_per_cell': 30, 'concurrency': 1,
                   'launches_per_device': 1, 'slo': 'first nonempty content TTFT p99 <=2000ms; error_rate<=1%',
                   'output_cap_is_not_actual_length': True,
                   'raw_SSE_timestamps_saved': True, 'content_chunk_not_necessarily_single_token': True,
                   'devices_not_sum_with_server': True, 'netio': self.a.netio,
                   'host_tag': self.a.host_tag, 'hf_cache': self.a.hf_cache,
                   'manifest_sha256': hashlib.sha256(Path(self.a.manifest).read_bytes()).hexdigest(),
                   'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                   'gpu_config': {'util': .9, 'max_model_len': 4096, 'seqs': 64, 'prefix_cache': False},
                   'hard_stop_kst': '20:55', 'standalone_docker': True,
                   'single_run_not_reproducibility_claim': True})
        self.empty()
        env = {'hostname': socket.gethostname(), 'kernel': command(['uname', '-a']),
               'gpu': command(['nvidia-smi', '-q']), 'npu': command(['furiosa-smi', 'info']),
               'cpu': Path('/proc/cpuinfo').read_text(), 'memory': Path('/proc/meminfo').read_text(),
               'netio_snapshot': get_json(self.a.netio), 'existing_containers': sorted(self.baseline_containers)}
        write_json(self.out / 'environment.json', env)
        idle_logger = Samplers(self.out / 'engine_off_idle', self.a.netio)
        self.update(state='engine_off_idle')
        idle_logger.start()
        try:
            self.pause(120)
        finally:
            idle_logger.close()
        idle = {s: mean(v for _, v in idle_logger.samples[s]) for s in ['server', 'gpu', 'npu']}
        write_json(self.out / 'engine_off_idle.json', idle)
        try:
            for device in ['gpu', 'npu']:
                model = self.start(device)
                self.pause(30)
                for inp, cap in CELLS:
                    self.cell(device, model, inp, cap, idle)
                self.cleanup(); self.pause(10)
            self.update(state='complete', ended_utc=utc())
        except BaseException as e:
            self.update(state='stopped_or_failed', reason=str(e), ended_utc=utc())
            raise
        finally:
            self.cleanup()


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--output', required=True); p.add_argument('--manifest', required=True)
    p.add_argument('--hf-cache', required=True); p.add_argument('--netio', required=True)
    p.add_argument('--host-tag', required=True, help='short label for this host, used in container and result names')
    a = p.parse_args()
    for sig in [signal.SIGTERM, signal.SIGINT]:
        signal.signal(sig, lambda *_: STOP.set())
    campaign = Campaign(a)
    try:
        campaign.run()
    except BaseException as exc:
        if campaign.created:
            campaign.update(state='stopped_or_failed', reason=str(exc), ended_utc=utc())
        raise


if __name__ == '__main__':
    main()
