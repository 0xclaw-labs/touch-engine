import type { ReactNode } from "react";

// 商家后台的数据表共用这一处表格。页面源不再直接写裸 table;
// 表体行由各页面以 tr/td 提供。
export function AdminDataTable({
  head,
  children,
}: {
  head: string[];
  children: ReactNode;
}) {
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
