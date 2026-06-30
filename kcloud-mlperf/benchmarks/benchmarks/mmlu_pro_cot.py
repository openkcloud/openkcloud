#!/usr/bin/env python3
"""
MMLU-Pro Benchmark - Official TIGER-Lab CoT Evaluation
5-shot Chain-of-Thought prompting with category-specific examples.

Based on: mmlu_pro/evaluate_from_local.py
"""
import os
import sys
import re
import time
import random
import torch
from collections import defaultdict
from datasets import load_dataset
from vllm import LLM, SamplingParams

# Official TIGER-Lab settings
random.seed(12345)
CHOICES = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J']
MAX_NEW_TOKENS = 2048  # Official value
K_SHOT = 5  # Number of few-shot examples


def select_by_category(data, category):
    """Select examples from the same category."""
    return [d for d in data if d['category'] == category]


def format_cot_example(example, include_answer=True):
    """Format a single example in official TIGER-Lab CoT format."""
    prompt = 'Question:\n' + example['question'] + '\nOptions:\n'
    for i, opt in enumerate(example['options']):
        prompt += f'{CHOICES[i]}. {opt}\n'
    if include_answer:
        cot = example.get('cot_content', '')
        if cot:
            cot = cot.replace("A: Let's think step by step.", "Answer: Let's think step by step.")
            prompt += cot + '\n\n'
    else:
        prompt += "Answer: Let's think step by step."
    return prompt


def generate_cot_prompt(val_data, curr, k=5):
    """Generate 5-shot CoT prompt with category-specific examples."""
    category = curr['category']
    # Official initial prompt format
    prompt = f'The following are multiple choice questions (with answers) about {category}. Think step by step and then finish your answer with "the answer is (X)" where X is the correct letter choice.\n\n'
    # Get k examples from same category
    same_cat = select_by_category(val_data, category)[:k]
    for ex in same_cat:
        prompt += format_cot_example(ex, include_answer=True)
    prompt += format_cot_example(curr, include_answer=False)
    return prompt


def extract_answer(text):
    """Official 3-tier answer extraction from TIGER-Lab."""
    # Try "answer is (X)" pattern first
    m = re.search(r'answer is \(?([A-J])\)?', text, re.I)
    if m:
        return m.group(1).upper()
    # Try "Answer: X" pattern
    m = re.search(r'[aA]nswer:\s*([A-J])', text)
    if m:
        return m.group(1).upper()
    # Last resort: find last letter A-J
    m = re.search(r'\b([A-J])\b(?!.*\b[A-J]\b)', text, re.DOTALL)
    if m:
        return m.group(0).upper()
    return None


def main():
    # Get sample split from environment
    sample_split = os.environ.get('SAMPLE_SPLIT', '')
    
    print('=' * 60, flush=True)
    print('MMLU-Pro Benchmark - Official TIGER-Lab Evaluation', flush=True)
    print('Method: 5-shot Chain-of-Thought (CoT)', flush=True)
    print('=' * 60, flush=True)
    
    # Load dataset
    print('[1/4] Loading MMLU-Pro dataset...', flush=True)
    dataset = load_dataset('TIGER-Lab/MMLU-Pro')
    if sample_split:
        test_data = list(dataset['test'])[:10]
    else:
        test_data = list(dataset['test'])
    val_data = list(dataset['validation'])
    total = len(test_data)
    print(f'  Test: {total}, Validation: {len(val_data)}', flush=True)
    
    # Preprocess: remove N/A options (official)
    for item in test_data + val_data:
        item['options'] = [o for o in item['options'] if o != 'N/A']
    
    # Load model
    print('[2/4] Loading model with vLLM...', flush=True)
    llm = LLM(
        model='meta-llama/Llama-3.1-8B-Instruct',
        dtype='float16',
        gpu_memory_utilization=0.9,
        max_model_len=4096,
        trust_remote_code=True
    )
    print(f'  GPU: {torch.cuda.get_device_name(0)}', flush=True)
    
    # Build prompts with few-shot examples
    print('[3/4] Running 5-shot CoT inference...', flush=True)
    prompts = []
    for item in test_data:
        prompts.append(generate_cot_prompt(val_data, item, k=K_SHOT))
    
    # Official sampling params
    sampling_params = SamplingParams(
        temperature=0,
        max_tokens=MAX_NEW_TOKENS,
        stop=['Question:']
    )
    
    t0 = time.time()
    outputs = llm.generate(prompts, sampling_params)
    predictions = [extract_answer(o.outputs[0].text) for o in outputs]
    elapsed = time.time() - t0
    
    # Compute results by category (official)
    print('[4/4] Computing results...', flush=True)
    category_stats = defaultdict(lambda: {'correct': 0, 'total': 0})
    correct = 0
    for i, item in enumerate(test_data):
        is_correct = predictions[i] == item['answer']
        if is_correct:
            correct += 1
            category_stats[item['category']]['correct'] += 1
        category_stats[item['category']]['total'] += 1
    
    acc = correct / total
    
    # Print results
    print('=' * 60, flush=True)
    print('RESULTS (Official TIGER-Lab Metrics)', flush=True)
    print('=' * 60, flush=True)
    for cat, stats in sorted(category_stats.items()):
        cat_acc = stats['correct'] / stats['total'] if stats['total'] > 0 else 0
        print(f'  {cat}: {cat_acc:.1%} ({stats["correct"]}/{stats["total"]})', flush=True)
    print('=' * 60, flush=True)
    print(f'Overall Accuracy: {acc:.2%} ({correct}/{total})', flush=True)
    print(f'Time: {elapsed/60:.1f}m | Throughput: {total/elapsed:.1f} q/s', flush=True)
    
    status = 'PASS' if acc >= 0.35 else 'FAIL'
    print(f'Status: {status} (Accuracy >= 35%)', flush=True)
    print('=' * 60, flush=True)
    
    return 0 if status == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
