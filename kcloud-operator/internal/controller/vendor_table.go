// ============================================================
// vendor_table.go: 벤더별 광고·배포 속성 표
// 상세: 벤더마다 다른 값(광고 선언·소유 모드·DRA 켜짐·DRA 드라이버명·DaemonSet 이름·전체 장치 리소스명)을
//       한 표에 모은다. 벤더별 분기는 이 표 한 곳에 두고, 함수들은 표를 읽는다.
// 생성일: 2026-10-13 | 수정일: 2026-10-13
// ============================================================

package controller

import (
	"context"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

type vendorAttrs struct {
	advertise  func(*npuv1alpha1.NPUClusterPolicy) npuv1alpha1.AdvertiseSpec
	owned      func(*npuv1alpha1.NPUClusterPolicy) bool
	draEnabled func(*npuv1alpha1.NPUClusterPolicy) bool
	driverName string
	dsName     string
	// resource 는 device-plugin 이 광고하는 전체 장치 리소스명이다. ctx 는 조회 실패 로깅에만 쓴다.
	resource func(context.Context, *npuv1alpha1.NPUClusterPolicy) string
}

// vendorTable 은 advertiseVendors() 의 다섯 벤더 키와 그 속성이다. 표에 없는 벤더는 표를 읽는
// 함수마다 기본값(owned 참, DRA 거짓, 이름·리소스명 빈 문자열)을 받는다.
var vendorTable = map[string]vendorAttrs{
	vendorNvidia: {
		advertise: func(p *npuv1alpha1.NPUClusterPolicy) npuv1alpha1.AdvertiseSpec {
			return p.Spec.Nvidia.AdvertiseSpec
		},
		owned:      func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Nvidia.IsOperatorOwned() },
		draEnabled: func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Nvidia.DRA.IsEnabled() },
		driverName: draDriverNameNvidia,
		dsName:     draDSNameNvidia,
		resource:   func(context.Context, *npuv1alpha1.NPUClusterPolicy) string { return nvidiaGPUResource },
	},
	// furiosa(warboy) 는 리소스명 오버라이드 필드가 없어 intent.WholeDeviceResource 카탈로그 값을 쓴다.
	vendorFuriosa: {
		advertise: func(p *npuv1alpha1.NPUClusterPolicy) npuv1alpha1.AdvertiseSpec {
			return p.Spec.Furiosa.AdvertiseSpec
		},
		owned:      func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Furiosa.IsOperatorOwned() },
		draEnabled: func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Furiosa.DRA.IsEnabled() },
		driverName: draDriverNameFuriosa,
		dsName:     draDSNameFuriosa,
		resource: func(ctx context.Context, _ *npuv1alpha1.NPUClusterPolicy) string {
			res, err := intent.WholeDeviceResource(vendorFuriosa, "warboy")
			if err != nil {
				logf.FromContext(ctx).Error(err, "furiosa(warboy) 리소스명 조회 실패", "vendor", vendorFuriosa)
			}
			return res
		},
	},
	// rngd 는 칩 세대는 다르지만 furiosa 와 DRA 드라이버·DaemonSet 이 하나라(draVendors 참고 —
	// Furiosa.Rngd.DRA 하나만 배포 대상이다) 같은 이름을 쓴다. RngdSpec 에 관리 모드가 없어 소유
	// 모드는 Furiosa 를 따르고, DRA 켜짐만 spec.furiosa.rngd.dra 를 따로 본다. 리소스명은 CRD
	// 필드가 있어도 공식 이미지가 무시해 실제 광고명과 무관하므로 카탈로그 값을 쓴다
	// (ensureFuriosaRngdDevicePlugin 참고).
	vendorRngd: {
		advertise: func(p *npuv1alpha1.NPUClusterPolicy) npuv1alpha1.AdvertiseSpec {
			return p.Spec.Furiosa.Rngd.AdvertiseSpec
		},
		owned:      func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Furiosa.IsOperatorOwned() },
		draEnabled: func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Furiosa.Rngd.DRA.IsEnabled() },
		driverName: draDriverNameFuriosa,
		dsName:     draDSNameFuriosa,
		resource: func(ctx context.Context, _ *npuv1alpha1.NPUClusterPolicy) string {
			res, err := intent.WholeDeviceResource(vendorFuriosa, "rngd")
			if err != nil {
				logf.FromContext(ctx).Error(err, "furiosa(rngd) 리소스명 조회 실패", "vendor", vendorRngd)
			}
			return res
		},
	},
	// tenstorrent·rebellions 는 spec 의 리소스명 오버라이드를 실제 렌더러가 반영하므로 그 필드를 읽는다.
	vendorTenstorrent: {
		advertise: func(p *npuv1alpha1.NPUClusterPolicy) npuv1alpha1.AdvertiseSpec {
			return p.Spec.Tenstorrent.AdvertiseSpec
		},
		owned:      func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Tenstorrent.IsOperatorOwned() },
		draEnabled: func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Tenstorrent.DRA.IsEnabled() },
		driverName: draDriverNameTenstorrent,
		dsName:     draDSNameTenstorrent,
		resource: func(_ context.Context, p *npuv1alpha1.NPUClusterPolicy) string {
			name := p.Spec.Tenstorrent.ResourceName
			if name == "" {
				name = ttResourceDefault
			}
			return name
		},
	},
	vendorRebellions: {
		advertise: func(p *npuv1alpha1.NPUClusterPolicy) npuv1alpha1.AdvertiseSpec {
			return p.Spec.Rebellions.AdvertiseSpec
		},
		owned:      func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Rebellions.IsOperatorOwned() },
		draEnabled: func(p *npuv1alpha1.NPUClusterPolicy) bool { return p.Spec.Rebellions.DRA.IsEnabled() },
		driverName: draDriverNameRebellions,
		dsName:     draDSNameRebellions,
		resource: func(_ context.Context, p *npuv1alpha1.NPUClusterPolicy) string {
			name := p.Spec.Rebellions.ResourceName
			if name == "" {
				name = rbllnsResourceNameDefault
			}
			prefix := p.Spec.Rebellions.ResourcePrefix
			if prefix == "" {
				prefix = rbllnsResourcePrefixDfault
			}
			return prefix + "/" + name
		},
	},
}
