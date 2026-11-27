/* 추가 브랜드 아이콘 — lucide-react에 없는 GitHub, Kubeflow 등 */
import * as React from "react";

export function GithubIcon({ className }: { className?: string }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" className={className}>
      <path d="M12 .5C5.4.5 0 5.9 0 12.5c0 5.3 3.4 9.8 8.2 11.3.6.1.8-.3.8-.6v-2.1c-3.3.7-4-1.6-4-1.6-.6-1.4-1.4-1.8-1.4-1.8-1.1-.8.1-.8.1-.8 1.2.1 1.9 1.3 1.9 1.3 1.1 1.9 2.9 1.4 3.6 1 .1-.8.4-1.4.8-1.7-2.7-.3-5.5-1.3-5.5-6 0-1.3.5-2.4 1.2-3.3-.1-.3-.5-1.6.1-3.3 0 0 1-.3 3.3 1.2 1-.3 2-.4 3-.4s2 .1 3 .4c2.3-1.6 3.3-1.2 3.3-1.2.7 1.7.3 3 .1 3.3.8.9 1.2 2 1.2 3.3 0 4.7-2.9 5.7-5.5 6 .4.4.8 1.1.8 2.2v3.3c0 .3.2.7.8.6A12 12 0 0 0 24 12.5C24 5.9 18.6.5 12 .5Z" />
    </svg>
  );
}

export function KubeflowIcon({ className }: { className?: string }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" className={className}>
      <path d="M12 2L4 7v10l8 5 8-5V7l-8-5Z" stroke="#326CE5" strokeWidth="1.8" strokeLinejoin="round" />
      <path d="M12 6.5l-4 2.5v6l4 2.5 4-2.5v-6l-4-2.5Z" fill="#326CE5" opacity="0.25" />
      <circle cx="12" cy="12" r="2.2" fill="#326CE5" />
    </svg>
  );
}
