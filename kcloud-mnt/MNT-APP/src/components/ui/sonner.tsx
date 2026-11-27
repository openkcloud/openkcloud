"use client";
import { useTheme } from "next-themes";
import { Toaster as Sonner, type ToasterProps } from "sonner";

const Toaster = ({ ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme();
  return (
    <Sonner
      theme={theme as ToasterProps["theme"]}
      className="toaster group"
      toastOptions={{
        classNames: {
          toast:
            "group toast group-[.toaster]:bg-card group-[.toaster]:text-foreground group-[.toaster]:border-border group-[.toaster]:shadow-lg",
          description: "group-[.toast]:text-muted-foreground",
          actionButton:
            "group-[.toast]:bg-primary group-[.toast]:text-primary-foreground",
          cancelButton:
            "group-[.toast]:bg-secondary group-[.toast]:text-secondary-foreground",
        },
      }}
      {...props}
    />
  );
};

/** 모달 바깥 상호작용이 토스트에서 일어났는지 — 모달(ui/dialog·ui/sheet)이 토스트 클릭에 닫히지 않게. */
function isToastEvent(e: CustomEvent<{ originalEvent: Event }>) {
  return !!(e.detail.originalEvent.target as Element | null)?.closest?.("[data-sonner-toaster]");
}

export { Toaster, isToastEvent };
