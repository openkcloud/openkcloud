# 벤더 적합성 시험 (test/conformance)

이 디렉터리는 "이 operator 에 벤더로 등록돼 있다" 가 무엇을 뜻하는지 기계로 검사하는
시험 하나를 담는다. 새 벤더를 추가하거나 기존 벤더를 고칠 때 등록 지점 하나를 빠뜨리면,
컴파일러는 아무 말도 하지 않지만 이 시험은 그 벤더와 그 계약을 지목해 실패한다.

## 이 시험이 지키는 것

이 시험은 **모든 능력을 지원하는지** 보지 않는다. 미지원(Unsupported)은 정상 상태다.
이 시험이 보는 것은 "벤더가 등록의 각 질문에 답은 했는가" 다. 계약은 열 개다. 여덟은
이 디렉터리 안에, 둘은 `internal/controller` 안에 있다(아래 "다른 패키지에 있는 계약
둘" 절 참조).

| # | 계약 | 시험 함수 |
|---|------|-----------|
| 1 | 능력 모델(`internal/vendorcap`)에 그 벤더 키가 있고 능력 15종 전부에 항목이 있다 | `TestEveryVendorSatisfiesConformance` |
| 2 | 각 능력 항목이 정직성 규칙을 지킨다 — `Native`·`Software` 는 `Source`(근거), `Unsupported` 는 `Reason`(사유), `Unknown` 은 아래 예외 목록에 등재 | `TestEveryVendorSatisfiesConformance` |
| 3 | `DriverAdapter`(`internal/driveradapter`)가 있고 자기 키를 정확히 돌려준다 | `TestEveryVendorSatisfiesConformance` |
| 4 | 자원 이름 사상(`internal/intent.WholeDeviceResource`)에 그 벤더가 있고, devicePlugin 을 지원한다고 선언한 벤더에만 이름이 있다 | `TestEveryVendorSatisfiesConformance` |
| 5 | `deploy/integration/image-manifest.yaml` 에 그 벤더 이미지가 등재돼 있다(air-gap 미러 대상) | `TestEveryVendorListedInImageManifest` |
| 6 | `deploy/helm/values.yaml` 에 그 벤더 블록이 있고, `enabled` 기본값 바로 다음 줄에 그 값을 설명하는 주석이 있다 | `TestEveryVendorHasDocumentedHelmDefault` |
| 7 | CRD 4종(`AcceleratorEvidence`·`AcceleratorPartitionPolicy`·`AcceleratorClass`·`AcceleratorOperation`)이 각자 독립적으로 선언한 Vendor enum 이 능력 모델의 벤더 집합과 같다 | `TestVendorEnumsAgreeAcrossCRDsAndRegistry` |
| 10 | 자원 이름 사상(`internal/intent.WholeDeviceResource`)이 돌려주는 리소스명이 `internal/intent.AcceleratorResourceDomains` 의 도메인 중 최소 하나와 겹친다 — 드라이버 업그레이드 drain 과 uninstall 게이트가 이 도메인 집합으로 가속기 사용 여부를 판정한다 | `TestEveryVendorResourceNameMatchesAnAcceleratorDomain` |

계약 7번의 CRD enum 값은 `config/crd/bases/` 아래 생성된 CRD YAML 에서 읽는다. Go 소스의
`+kubebuilder:validation:Enum` 마커 주석이 아니라 이 YAML 을 읽는 이유는, API 서버가 실제로
강제하는 값이 이 YAML 이기 때문이다. 마커 주석과 생성물이 어긋나 있어도(`make manifests`
를 돌리지 않은 경우) 이 시험은 실제로 클러스터가 보는 값을 기준으로 판정한다.

벤더 집합은 `internal/vendorcap.Keys()` 의 `Vendor` 필드에서 중복을 제거해 만든다. Furiosa
처럼 한 벤더 아래 제품이 둘(`rngd`·`warboy`)이어도 벤더로는 한 번만 센다. image-manifest·
Helm values·CRD enum 은 모두 product 가 아니라 vendor 단위로 조직돼 있다.

## 다른 패키지에 있는 계약 둘

벤더 등록 지점 중 둘은 `internal/controller` 패키지의 비공개(unexported) 식별자를 직접
본다. 그 식별자를 보려고 새 export 를 만드는 대신 그 패키지 자신의 시험 파일에 뒀다.
`DriverAdapter` 인터페이스에 시험 전용 메서드를 더하지 않는 것과 같은 이유다.

| # | 계약 | 위치 |
|---|------|------|
| 8 | `isAcceleratorNode`(`AcceleratorHealth` Reconcile 의 첫 게이트)가 등록된 모든 벤더의 `kcloud.ai/<vendor>.present` 라벨을 인식한다 | `internal/controller/acceleratorhealth_vendor_registration_test.go` → `TestIsAcceleratorNodeRecognizesEveryVendorPresentLabel` |
| 9 | `acceleratorOperationVendors` 허용 목록의 키 집합이 `AcceleratorOperationSpec.Vendor` 의 CRD enum(계약 7번과 같은 근거지)과 같다 | `internal/controller/acceleratorhealth_vendor_registration_test.go` → `TestAcceleratorOperationVendorsMatchesCRDEnum` |

`make conformance` 는 이 둘을 `go test ./internal/controller/...` 전체 대신 `-run` 으로
이 두 함수만 지정해 실행한다. `-run` 으로 `TestControllers`(Ginkgo 스위트·`BeforeSuite`
리소스 생성)는 건너뛰지만, 패키지의 `TestMain` 이 여는 envtest 는 그대로 필요하다
(`KUBEBUILDER_ASSETS`). 두 시험 자체는 그 환경 위에서 즉시 끝난다. 표준 검증 경로인
`go test ./internal/controller/...`(`make test`) 는 전체 스위트와 함께 이 둘도 매번 돈다.

node-device-detector 의 `computeLabels` 는 장치가 하나라도 있으면 `kcloud.ai/accelerator.present`
(와 옛 이름 `kcloud.ai/gpu.present`, 0.9 에서 제거)를 항상 함께 쓰고, 이 라벨은
`isAcceleratorNode` 의 집합에 이미 있다. 그래서 벤더별
present 라벨이 그 switch 에 없어도 노드 자체가 감시 대상에서 빠지지는 않는다. 계약 8번이
실제로 지키는 것은 벤더별 present 라벨의 리터럴 목록 완전성이다.

## 실행 방법

열 계약 전부를 보려면 이 한 명령이다.

```bash
make conformance
```

이 타깃은 두 줄이다. 계약 1~7·10 번은 `go test ./test/conformance/... -v`, 8·9 번은
`go test ./internal/controller/... -run 'TestIsAcceleratorNodeRecognizesEveryVendorPresentLabel|TestAcceleratorOperationVendorsMatchesCRDEnum' -count=1 -v`
(위 "다른 패키지에 있는 계약 둘" 절 참조 — `KUBEBUILDER_ASSETS` 는 필요하지만 두 시험
자체는 그 환경 위에서 즉시 끝난다).

계약 1~7·10 번만 보려면 `test/conformance` 패키지를 직접 실행한다.

```bash
go test ./test/conformance/... -v
```

특정 계약만 보려면 `-run` 으로 해당 시험 함수를 지정한다. 예를 들어 계약 7번만 보려면:

```bash
go test ./test/conformance/... -run TestVendorEnumsAgreeAcrossCRDsAndRegistry -v
```

## 예외를 추가하는 방법

계약 2번은 능력 상태가 `Unknown` 인 항목을 자동으로 통과시키지 않는다. `Unknown` 이
남아 있으려면 `conformance_test.go` 의 `unknownExceptions` 맵에 그 (벤더, 능력) 쌍과 사유를
함께 등재해야 한다. 지금 이 맵은 비어 있다. 레지스트리에 `Unknown` 상태 항목이 하나도
없기 때문이다(마지막 항목이던 rebellions/CapIsolation 이 `Unsupported`+`AssuranceHardware`
로 확정돼 있다). 다음에 새 벤더·능력을 조사 전에
등록해 `Unknown` 이 생기면 아래 형태(벤더·능력 이름은 예시)로 등재한다.

```go
var unknownExceptions = map[capKey]string{
	{Key: vendorcap.Key{Vendor: "example-vendor"}, Cap: vendorcap.CapReset}: "예: 측정에 필요한 하드웨어 부재로 확정 보류 — 확보되면 조사 후 이 항목을 지운다",
}
```

목록에 없는 `Unknown` 이 새로 생기면 이 시험이 해당 벤더·능력을 지목해 실패한다. 이때 할
일은 둘 중 하나다.

- 조사해서 상태를 `Native`·`Software`·`Unsupported` 중 하나로 확정한다(근거·사유 없이
  등급을 올리는 것은 `internal/vendorcap` 의 정직성 원칙 위반이다).
- 확정할 수 없는 구체적인 이유(예: 측정에 필요한 하드웨어·노드가 없다)가 있으면
  `unknownExceptions` 에 그 이유를 적어 등재한다.

**예외는 통과시키는 방법이 아니라 갚아야 할 빚이다.** 목록에 이유를 적어 두는 것은 "이 항목을
잊지 않았다" 는 뜻이지 "이 항목을 몰라도 된다" 는 뜻이 아니다. 예외 사유가 사라지면(예: 막혀
있던 노드가 복구되면) 그 항목을 조사해 확정하고 목록에서 지운다.

예외 목록이 있는 계약은 2번뿐이다. 나머지(1·3·4·5·6·7·8·9·10)는 "있다/없다" 가 명확한
사실이라 예외로 덮을 여지가 없다. 이 계약들이 실패하면 등록 지점 자체를 채워야 한다.

## 시험이 실제로 잡는지 확인한 방법

각 계약의 시험은 그 등록 지점(능력 항목, `Source`·`Reason`, `DriverAdapter` 등록, 자원
이름, 이미지 매니페스트 항목, Helm 주석, CRD enum 의 벤더, `isAcceleratorNode` switch 의
라벨, `acceleratorOperationVendors` 의 키, `AcceleratorResourceDomains` 의 도메인)를
하나씩 비웠을 때 그 벤더·계약만 정확히 지목해 실패하는 것으로 검증돼 있다.
