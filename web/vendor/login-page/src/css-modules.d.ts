/** CSS Modules 最小类型：类名 → 作用域化类名（宿主 Next.js 编译期替换）。 */
declare module "*.module.css" {
  const classes: { readonly [key: string]: string };
  export default classes;
}
