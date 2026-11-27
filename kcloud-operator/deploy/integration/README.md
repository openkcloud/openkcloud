<!-- README.md: Magnum CAPI Add-on 연동 자료(kcloud-operator 측 제공물) 안내 | 생성일: 2026-09-10 | 수정일: 2026-10-28 -->

# Magnum CAPI 연동 자료

`kcloud-magnum-capi-helm` 이 Service K8s 클러스터에 kcloud-operator 를 자동 설치할 때 필요한 값을 모아 둔 디렉터리다. 릴리스 태그마다 같이 갱신된다.

| 파일 | 내용 |
|---|---|
| `release.yaml` | 차트 주소·Release 이름·namespace·K8s 버전별 차트 버전과 고정 values URL·입력·사이트 값·helm 호출 예 |
| `image-manifest.yaml` | 이 릴리스가 쓰는 이미지 전체(조건 포함), 완성 경로 필드, 드라이버 apt 저장소 |
| `examples/site-values.example.yaml` | Add-on 이 만들 `site-values.yaml` 의 본보기 |

## 설치 형태

```bash
helm upgrade --install kcloud-operator oci://ghcr.io/openkcloud/charts/kcloud-operator \
  --version <chartVersion> -n kcloud --create-namespace \
  --reset-values \
  -f <valuesURL> \
  -f site-values.yaml \
  --wait --wait-for-jobs --timeout 15m
```

`values-k8s1.34.yaml` 은 차트 안의 프리셋이다. helm 의 `-f` 는 URL 을 받으므로 차트를 풀지 않고 raw URL 을 그대로 준다. `<chartVersion>` 과 `<valuesURL>` 에는 `release.yaml` 에서 대상 클러스터의 K8s 버전에 맞는 프로파일 값을 넣는다. `valuesURL` 은 커밋 SHA 로 고정돼 있어 같은 주소가 늘 같은 내용을 돌려준다. 같은 파일의 `valuesBrowseURL` 은 브랜치 주소로, 최신 내용을 사람이 확인할 때만 쓴다. Add-on CR 에 값을 인라인으로 넣는 방식이면 그 파일 내용을 옮긴다. Service K8s 가 1.26~1.30 이면 차트 `0.6.3` 과 `values-k8s1.28.yaml`(태그 `v0.6.3-k8s1.28`)을 쓴다. 그 프리셋은 차트 `0.6.3` 을 담은 `release/k8s-1.28` 브랜치에 있다 — 차트 `0.8.4` 에는 없고, `0.8.4` 는 `kubeVersion` 으로 1.31 미만을 거절한다. 두 프로파일의 `valuesURL` 이 각자 그 라인의 커밋을 가리키므로 Add-on 이 받는 주소는 달라지지 않는다. 업그레이드도 같은 명령으로 값을 전부 다시 넘긴다. hook 을 생략하면 CRD 가 갱신되지 않으므로 `--no-hooks` 는 쓰지 않는다.

`site-values.yaml` 은 저장소에 있는 파일이 아니다. Add-on 이 설치 직전에 대상 환경에 맞게 만드는 로컬 파일이며, 본보기가 `examples/site-values.example.yaml` 이다. 공개망이라 덮어쓸 값이 없어도 helm 이 `-f` 대상 파일을 찾지 못하면 실패하므로 반드시 만든다. 파일 이름은 `release.yaml` 의 `helm.siteValuesFile` 이 정한다.

## 입력

Magnum label 은 `kcloud_operator_enabled` 하나다. 벤더 선택은 받지 않는다. node-manager 가 노드의 PCI 장치를 읽어 `kcloud.ai/<vendor>.present` 라벨을 붙이고, device-plugin·드라이버 설치 Job·exporter 는 그 라벨이 있는 노드에만 배치된다. 장치가 없는 벤더는 Pod 가 생기지 않는다. kcloud-operator 를 켜면 NVIDIA GPU Operator 는 꺼야 한다. 드라이버·Container Toolkit·device-plugin·dcgm-exporter 를 kcloud-operator 가 직접 관리하기 때문이다.

## 삭제

```bash
helm uninstall kcloud-operator -n kcloud --wait
```

`pre-delete` hook 이 가속기 자원(`nvidia.com/*`, `furiosa.ai/*`, `beta.furiosa.ai/*`, `tenstorrent.com/*`, `rebellions.ai/*`, DRA claim)을 쓰는 Pod 를 확인한다. 하나라도 있으면 uninstall 이 실패하고 아무것도 지워지지 않는다. Pod 목록은 `kubectl -n kcloud logs job/kcloud-operator-uninstall-gate` 에 있다. 없으면 정책 CR, operator 가 만든 DaemonSet·Job, CRD 까지 전부 삭제된다. 호스트의 드라이버 패키지는 지우지 않는다.

## 이미지 접근

기본값은 kcloud 이미지를 `ghcr.io/openkcloud` 에서, 벤더 이미지를 각 벤더의 공개 레지스트리에서 받는다. 자체 미러를 쓰려면 `global.registry`·`global.vendorRegistry` 와 `image-manifest.yaml` 의 `fullPathValues` 다섯 필드를 미러 경로로 지정한다. 드라이버는 이미지가 아니라 노드가 apt 로 받으므로 `aptRepositories` 의 저장소 도달성(또는 apt 미러)과 Warboy 인증 Secret 이 별도로 필요하다.
