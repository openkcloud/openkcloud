#!/usr/bin/env python3
"""
MLPerf Inference Benchmark - CNN/DailyMail Summarization
Official MLCommons evaluation with vLLM backend.

Based on: mlcommons_inference/language/llama3.1-8b/evaluation.py
"""
import os
import sys
import time
import torch
import nltk
from datasets import load_dataset
from vllm import LLM, SamplingParams
import evaluate


def main():
    # Get sample split from environment (empty string for full dataset)
    sample_split = os.environ.get('SAMPLE_SPLIT', '')
    
    print('=' * 60, flush=True)
    print('MLPerf Inference Benchmark - Official Evaluation', flush=True)
    print('Model: meta-llama/Llama-3.1-8B-Instruct', flush=True)
    print('Backend: vLLM (batched inference)', flush=True)
    print('=' * 60, flush=True)
    
    # Load dataset
    print('[1/4] Loading dataset (CNN/DailyMail)...', flush=True)
    load_start = time.time()
    dataset = load_dataset('cnn_dailymail', '3.0.0', split=f'test{sample_split}')
    total = len(dataset)
    print(f'  Dataset loaded: {total} samples in {time.time()-load_start:.1f}s', flush=True)
    
    # Load model
    print('[2/4] Loading model with vLLM...', flush=True)
    model_start = time.time()
    llm = LLM(
        model='meta-llama/Llama-3.1-8B-Instruct',
        dtype='float16',
        gpu_memory_utilization=0.9,
        max_model_len=4096,
        trust_remote_code=True
    )
    print(f'  Model loaded in {time.time()-model_start:.1f}s', flush=True)
    print(f'  GPU: {torch.cuda.get_device_name(0)}', flush=True)
    
    # Prepare prompts
    print('[3/4] Running inference with vLLM batching...', flush=True)
    prompts, refs = [], []
    for s in dataset:
        article = s['article'][:2000]
        prompts.append(f'Summarize the following article in a few sentences.\n\nArticle: {article}\n\nSummary:')
        refs.append(s['highlights'])
    
    sampling_params = SamplingParams(temperature=0, max_tokens=150, stop=['\n\n'])
    
    # Batch inference
    t0 = time.time()
    batch_size = 32
    preds = []
    for i in range(0, len(prompts), batch_size):
        batch = prompts[i:i+batch_size]
        outputs = llm.generate(batch, sampling_params)
        for output in outputs:
            preds.append(output.outputs[0].text.strip())
        done = min(i + batch_size, len(prompts))
        elapsed = time.time() - t0
        rate = done / elapsed if elapsed > 0 else 0
        eta = (total - done) / rate / 60 if rate > 0 else 0
        print(f'  [{done}/{total}] {done*100/total:.1f}% | {rate:.1f} samples/s | ETA: {eta:.0f}m', flush=True)
    
    elapsed = time.time() - t0
    print(f'  Completed {total} samples in {elapsed/60:.1f} minutes', flush=True)
    
    # Compute ROUGE scores (official postprocessing)
    print('[4/4] Computing ROUGE scores...', flush=True)
    
    def postprocess(preds, targets):
        """Official MLCommons postprocessing with nltk sentence tokenization."""
        preds = ['\n'.join(nltk.sent_tokenize(p.strip())) for p in preds]
        targets = ['\n'.join(nltk.sent_tokenize(t.strip())) for t in targets]
        return preds, targets
    
    preds_p, refs_p = postprocess(preds, refs)
    rouge = evaluate.load('rouge')
    result = rouge.compute(predictions=preds_p, references=refs_p, use_stemmer=True)
    
    # Print results
    print('=' * 60, flush=True)
    print('RESULTS (Official MLCommons Metrics)', flush=True)
    print(f"ROUGE-1: {result['rouge1']:.4f}", flush=True)
    print(f"ROUGE-2: {result['rouge2']:.4f}", flush=True)
    print(f"ROUGE-L: {result['rougeL']:.4f}", flush=True)
    print(f'Samples: {total} | Time: {elapsed/60:.1f}m | Throughput: {total/elapsed:.2f} samples/s', flush=True)
    
    status = 'PASS' if result['rougeL'] >= 0.15 else 'FAIL'
    print(f'Status: {status} (ROUGE-L >= 0.15)', flush=True)
    print('=' * 60, flush=True)
    
    return 0 if status == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
