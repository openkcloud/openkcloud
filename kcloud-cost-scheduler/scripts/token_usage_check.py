#!/usr/bin/env python3
import argparse
import json
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--result", required=True)
    p.add_argument("--out", required=True)
    args = p.parse_args()
    d = json.loads(Path(args.result).read_text(encoding="utf-8"))
    ok = [r for r in d["results"] if r.get("status") == "ok"]
    ins = [r.get("prompt_tokens") for r in ok]
    outs = [r.get("completion_tokens") for r in ok]
    rep = {
        "n_ok": len(ok),
        "n_requested": d["n_requested"],
        "error_rate": d["error_rate"],
        "prompt_tokens_all_149": all(x == 149 for x in ins) and len(ins) == d["n_requested"],
        "completion_eq_128_count": sum(1 for x in outs if x == 128),
        "ttft_p50_ms": d.get("ttft_p50_ms"),
        "ttft_p99_ms": d.get("ttft_p99_ms"),
        "duration_s": d["duration_s"],
        "requests_sha256": d["requests_file_sha256"],
    }
    Path(args.out).write_text(json.dumps(rep, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(rep, indent=2))


if __name__ == "__main__":
    main()
