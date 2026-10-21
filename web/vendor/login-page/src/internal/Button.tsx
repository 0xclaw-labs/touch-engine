"use client";

import type { ReactNode } from "react";

import styles from "./ui.module.css";

export type ButtonVariant = "primary" | "secondary";

export type ButtonProps = {
  variant?: ButtonVariant;
  /** 通栏按钮：登录表单主操作占满一行。 */
  block?: boolean;
  loading?: boolean;
  disabled?: boolean;
  type?: "button" | "submit";
  children: ReactNode;
  onClick?: () => void;
};

function cx(...parts: (string | false | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

/** primary=accent 实底+光晕、secondary=raised+描边；loading 三点脉冲（goboost-fd Button 子集）。 */
export function Button({
  variant = "secondary",
  block = false,
  loading = false,
  disabled,
  type = "button",
  children,
  onClick,
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cx(styles.btn, styles[variant], block && styles.block)}
      disabled={disabled ?? loading}
      aria-busy={loading || undefined}
      onClick={onClick}
    >
      {loading ? (
        <span className={styles.dots} aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
      ) : null}
      <span className={styles.label}>{children}</span>
    </button>
  );
}
