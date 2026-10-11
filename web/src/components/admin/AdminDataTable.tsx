import type { ReactNode } from "react";

// 商家后台的两张数据表共用这一处表格。页面源不再直接写 table。
export function AdminDataTable({ head, children }: { head: string[]; children: ReactNode }) {
  return (
    <table className="tk-admin-table">
      <thead>
        <tr>
          {head.map((label) => (
            <th key={label}>{label}</th>
          ))}
        </tr>
      </thead>
      <tbody>{children}</tbody>
    </table>
  );
}
