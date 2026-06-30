#!/usr/bin/env python3
"""
LLM Inference Throughput Benchmark
vLLM backend performance test with single and batch prompts.
"""
import sys
import time
import torch
from vllm import LLM, SamplingParams


def main():
    print('=' * 60, flush=True)
    print('LLM Inference Benchmark', flush=True)
    print('=' * 60, flush=True)
    
    # Load model
    print('[1/3] Loading model with vLLM...', flush=True)
    t0 = time.time()
    llm = LLM(
        model='meta-llama/Llama-3.1-8B-Instruct',
        dtype='float16',
        gpu_memory_utilization=0.9,
        max_model_len=4096,
        trust_remote_code=True
    )
    print(f'  Model loaded in {time.time()-t0:.1f}s', flush=True)
    print(f'  GPU: {torch.cuda.get_device_name(0)}', flush=True)
    
    # Single prompt test
    print('[2/3] Single prompt test...', flush=True)
    prompt = 'How to make spaghetti from scratch? Provide a detailed step-by-step recipe.'
    print(f'Prompt: {prompt}', flush=True)
    
    params = SamplingParams(temperature=0.7, max_tokens=512)
    t0 = time.time()
    outputs = llm.generate([prompt], params)
    elapsed = time.time() - t0
    
    tokens = len(outputs[0].outputs[0].token_ids)
    response_text = outputs[0].outputs[0].text
    print(f'\nResponse: {response_text[:500]}...', flush=True)
    print(f'\nTokens: {tokens} | Time: {elapsed:.2f}s | {tokens/elapsed:.1f} tok/s', flush=True)
    
    # Batch throughput test
    print('[3/3] Batch throughput test...', flush=True)
    prompts = [
        'What is AI?',
        'Explain quantum computing.',
        'Write a haiku.',
        'Benefits of exercise?'
    ]
    
    t0 = time.time()
    batch_out = llm.generate(prompts, SamplingParams(temperature=0, max_tokens=256))
    batch_elapsed = time.time() - t0
    
    total_tokens = sum(len(o.outputs[0].token_ids) for o in batch_out)
    print(f'Batch: {len(prompts)} prompts, {total_tokens} tokens in {batch_elapsed:.2f}s', flush=True)
    print(f'Throughput: {total_tokens/batch_elapsed:.1f} tok/s', flush=True)
    
    print('=' * 60, flush=True)
    print('Status: PASS', flush=True)
    print('=' * 60, flush=True)
    
    return 0


if __name__ == '__main__':
    sys.exit(main())
