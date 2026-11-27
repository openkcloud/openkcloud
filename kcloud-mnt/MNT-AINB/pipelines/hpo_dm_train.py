"""HPO 시도(Trial) 1회가 실행하는 실제 학습 스크립트.

Katib이 학습률·배치 크기를 바꿔가며 이 스크립트를 반복 실행하고,
마지막에 찍히는 accuracy=... 를 읽어 최적 조합을 찾는다.

계산식으로 점수를 흉내내는 게 아니라 DM코드 이미지 5,651장으로
ResNet18을 실제로 학습시킨다.

  실행: python3 train.py <학습률> <배치크기> <epoch수> <데이터셋 URI>
"""
import sys
import os
import json
import tarfile
import time

import torch
import torch.nn as nn
from torch.utils.data import Dataset, DataLoader
from torchvision import transforms
from torchvision.models import resnet18, ResNet18_Weights
from PIL import Image
from minio import Minio

LR = float(sys.argv[1])
BATCH = int(sys.argv[2])
EPOCHS = int(sys.argv[3]) if len(sys.argv) > 3 else 3
DATASET = sys.argv[4] if len(sys.argv) > 4 else "s3://datasets/dm-code/dm_dataset.tgz"
IMG = 128
SEED = 42

print(f"[trial] lr={LR} batch={BATCH} epochs={EPOCHS}", flush=True)

# ── 데이터 준비 ────────────────────────────────────────────────
bucket, key = DATASET.replace("s3://", "").split("/", 1)
cli = Minio(os.environ["S3_ENDPOINT"], access_key=os.environ["S3_KEY"],
            secret_key=os.environ["S3_SECRET"], secure=False)
cli.fget_object(bucket, key, "/tmp/ds.tgz")
with tarfile.open("/tmp/ds.tgz") as t:
    t.extractall("/tmp/data")

ok = sorted(f"qr_crops/{f}" for f in os.listdir("/tmp/data/qr_crops")
            if f.endswith(".jpg"))
ng = sorted(f"qr_crops_failed/{f}" for f in os.listdir("/tmp/data/qr_crops_failed")
            if f.endswith(".jpg"))

import random
rows = [(p, 0) for p in ok] + [(p, 1) for p in ng]
random.Random(SEED).shuffle(rows)          # 시도마다 같은 분할을 써야 비교가 된다
n_val = int(len(rows) * 0.2)
val_rows, train_rows = rows[:n_val], rows[n_val:]
print(f"[trial] 학습 {len(train_rows)}장 / 검증 {len(val_rows)}장", flush=True)

norm = transforms.Normalize([0.485, 0.456, 0.406], [0.229, 0.224, 0.225])
train_tf = transforms.Compose([
    transforms.Resize((IMG, IMG)),
    transforms.RandomHorizontalFlip(),
    transforms.RandomRotation(10),
    transforms.ColorJitter(brightness=0.2, contrast=0.2),
    transforms.ToTensor(), norm,
])
eval_tf = transforms.Compose([
    transforms.Resize((IMG, IMG)), transforms.ToTensor(), norm,
])


class DM(Dataset):
    def __init__(self, rows, tf):
        self.rows, self.tf = rows, tf

    def __len__(self):
        return len(self.rows)

    def __getitem__(self, i):
        rel, y = self.rows[i]
        return self.tf(Image.open(f"/tmp/data/{rel}").convert("RGB")), y


tr = DataLoader(DM(train_rows, train_tf), batch_size=BATCH, shuffle=True,
                num_workers=2)
va = DataLoader(DM(val_rows, eval_tf), batch_size=BATCH, num_workers=2)

# ── 학습 ──────────────────────────────────────────────────────
device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
print(f"[trial] 장비: {device}", flush=True)

net = resnet18(weights=ResNet18_Weights.DEFAULT)
net.fc = nn.Linear(net.fc.in_features, 2)
net = net.to(device)
crit = nn.CrossEntropyLoss()
opt = torch.optim.AdamW(net.parameters(), lr=LR)

best = 0.0
for ep in range(1, EPOCHS + 1):
    t0 = time.time()
    net.train()
    for x, y in tr:
        x, y = x.to(device), y.to(device)
        opt.zero_grad()
        crit(net(x), y).backward()
        opt.step()

    net.eval()
    hit = tot = 0
    with torch.no_grad():
        for x, y in va:
            x, y = x.to(device), y.to(device)
            hit += (net(x).argmax(1) == y).sum().item()
            tot += y.size(0)
    acc = hit / tot
    best = max(best, acc)
    print(f"[trial] epoch {ep}/{EPOCHS} 검증 정확도 {acc*100:.2f}% "
          f"({time.time()-t0:.0f}초)", flush=True)

# Katib 수집기가 읽는 줄 — 이 형식이어야 인식된다
print(f"accuracy={best:.4f}", flush=True)
time.sleep(5)   # 사이드카가 마지막 줄을 읽을 시간
