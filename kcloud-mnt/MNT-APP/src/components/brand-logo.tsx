/* 모델 작성자 브랜드 로고 — SVG inline.
 * 핵심: Meta, Alibaba, Google, Mistral, Microsoft, LG, Upstage, OpenAI, Stability, BAAI, NVIDIA, Salesforce */
import * as React from "react";
import { cn } from "@/lib/utils";

interface BrandLogoProps {
  author: string;
  size?: number;
  className?: string;
}

export function BrandLogo({ author, size = 24, className }: BrandLogoProps) {
  const a = author.toLowerCase();
  const props = { width: size, height: size, viewBox: "0 0 24 24" as const, className: cn("shrink-0", className) };

  // Meta — 파란색 ∞
  if (a.includes("meta")) {
    return (
      <svg {...props} fill="none">
        <path d="M8.5 6c-2.7 0-4.5 2.2-4.5 5 0 3.3 1.7 6 4.5 6 1.6 0 2.5-.8 3.5-2.3l1-1.4c1 1.5 2 3.7 4.5 3.7 2.7 0 4.5-2.7 4.5-6 0-2.8-1.8-5-4.5-5-1.7 0-2.9 1-4 2.6L12 10.5l-1-1.6C9.9 7.3 8.7 6 8.5 6Z" stroke="#0866FF" strokeWidth="2.2" strokeLinecap="round" />
      </svg>
    );
  }

  // Alibaba — 주황 A
  if (a.includes("alibaba")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="4" fill="#FF6A00" />
        <text x="12" y="16.5" fontSize="14" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="white">阿</text>
      </svg>
    );
  }

  // Google — 4색 G
  if (a.includes("google")) {
    return (
      <svg {...props} fill="none">
        <path d="M21.6 12.2c0-.7-.1-1.3-.2-2H12v3.8h5.4c-.2 1.3-1 2.4-2 3.1v2.5h3.2c1.9-1.7 3-4.3 3-7.4Z" fill="#4285F4" />
        <path d="M12 22c2.7 0 5-1 6.6-2.4l-3.2-2.5c-.9.6-2 1-3.4 1-2.6 0-4.8-1.8-5.6-4.1H3v2.5C4.7 19.8 8.1 22 12 22Z" fill="#34A853" />
        <path d="M6.4 14c-.4-1-.4-2.1 0-3.1V8.4H3a10 10 0 0 0 0 9.1l3.4-2.5Z" fill="#FBBC04" />
        <path d="M12 5.4c1.5 0 2.8.5 3.8 1.5L18.7 4C17 2.5 14.7 1.5 12 1.5c-3.9 0-7.3 2.2-9 5.5L6.4 9.4c.8-2.3 3-4 5.6-4Z" fill="#EA4335" />
      </svg>
    );
  }

  // Mistral — 주황색 격자
  if (a.includes("mistral")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="3" fill="#000000" />
        <rect x="5" y="5" width="3.5" height="3.5" fill="#FF7000" />
        <rect x="10.25" y="5" width="3.5" height="3.5" fill="#FFA600" />
        <rect x="15.5" y="5" width="3.5" height="3.5" fill="#FFCE00" />
        <rect x="5" y="10.25" width="3.5" height="3.5" fill="#FF7000" />
        <rect x="10.25" y="10.25" width="3.5" height="3.5" fill="#FFA600" />
        <rect x="15.5" y="10.25" width="3.5" height="3.5" fill="#FFCE00" />
        <rect x="5" y="15.5" width="3.5" height="3.5" fill="#FF7000" />
        <rect x="10.25" y="15.5" width="3.5" height="3.5" fill="#FFA600" />
        <rect x="15.5" y="15.5" width="3.5" height="3.5" fill="#FFCE00" />
      </svg>
    );
  }

  // Microsoft — 4색 사각형
  if (a.includes("microsoft")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="9.4" height="9.4" fill="#F25022" />
        <rect x="12.6" y="2" width="9.4" height="9.4" fill="#7FBA00" />
        <rect x="2" y="12.6" width="9.4" height="9.4" fill="#00A4EF" />
        <rect x="12.6" y="12.6" width="9.4" height="9.4" fill="#FFB900" />
      </svg>
    );
  }

  // LG AI Research — 빨강 L
  if (a.includes("lg")) {
    return (
      <svg {...props}>
        <circle cx="12" cy="12" r="10" fill="#A50034" />
        <text x="12" y="16.5" fontSize="11" fontWeight="700" fontFamily="system-ui" textAnchor="middle" fill="white">LG</text>
      </svg>
    );
  }

  // Upstage — 보라 U
  if (a.includes("upstage")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="4" fill="#6C5CE7" />
        <text x="12" y="16.5" fontSize="13" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="white">U</text>
      </svg>
    );
  }

  // OpenAI — 검은 꽃
  if (a.includes("openai")) {
    return (
      <svg {...props}>
        <circle cx="12" cy="12" r="10" fill="#000000" />
        <path d="M16 9.5a3.5 3.5 0 0 0-6.6-1.7 3.5 3.5 0 0 0-2.4 6 3.5 3.5 0 0 0 6.6 1.7 3.5 3.5 0 0 0 2.4-6Z" fill="none" stroke="#10A37F" strokeWidth="1.4" />
      </svg>
    );
  }

  // Stability AI — 검은 S
  if (a.includes("stability")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="4" fill="#1A1A1A" />
        <path d="M8 9c0-1.5 1.5-2.5 3-2.5 1.7 0 3 .9 3 2.5 0 1.5-1.4 2-3 2.5-1.7.5-3 1-3 2.5 0 1.5 1.3 2.5 3 2.5 1.5 0 3-1 3-2.5" stroke="white" strokeWidth="1.6" strokeLinecap="round" fill="none" />
      </svg>
    );
  }

  // BAAI — 파란 B
  if (a.includes("baai")) {
    return (
      <svg {...props}>
        <circle cx="12" cy="12" r="10" fill="#003DA5" />
        <text x="12" y="16.5" fontSize="13" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="white">智</text>
      </svg>
    );
  }

  // NVIDIA — 녹색
  if (a.includes("nvidia")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="3" fill="#76B900" />
        <text x="12" y="16" fontSize="9" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="white">NV</text>
      </svg>
    );
  }

  // Salesforce — 파란 구름
  if (a.includes("salesforce")) {
    return (
      <svg {...props} fill="none">
        <path d="M9 7c-3 0-5 2-5 4.5C2.5 12 1.5 13.5 1.5 15c0 2.3 2 4 4.5 4h11c2 0 3.5-1.5 3.5-3.5 0-1.6-1-3-2.5-3.5.2-2.5-1.7-4-4-4-1 0-2 .3-2.8.9C10.5 7.5 9.8 7 9 7Z" fill="#00A1E0" />
      </svg>
    );
  }

  // Bytedance — 검정
  if (a.includes("bytedance")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="4" fill="#000000" />
        <text x="12" y="16" fontSize="10" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="white">字节</text>
      </svg>
    );
  }

  // MIT — 검정 + 적색
  if (a.includes("mit")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="3" fill="#A31F34" />
        <text x="12" y="16" fontSize="9" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="white">MIT</text>
      </svg>
    );
  }

  // Ultralytics
  if (a.includes("ultralytics")) {
    return (
      <svg {...props}>
        <rect x="2" y="2" width="20" height="20" rx="4" fill="#111F2C" />
        <text x="12" y="16" fontSize="8" fontWeight="800" fontFamily="system-ui" textAnchor="middle" fill="#0BD5F0">YOLO</text>
      </svg>
    );
  }

  // 기본: 작성자 첫 글자
  const initials = author
    .split(/\s+/)
    .map((w) => w[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
  return (
    <svg {...props}>
      <rect x="2" y="2" width="20" height="20" rx="4" fill="var(--brand-indigo-500)" />
      <text x="12" y="16" fontSize="10" fontWeight="700" fontFamily="system-ui" textAnchor="middle" fill="white">
        {initials}
      </text>
    </svg>
  );
}
