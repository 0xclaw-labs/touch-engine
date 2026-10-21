// Execute the real TSX component and its event callbacks with deterministic hook state.
// DOM/layout/browser acceptance remains separate; only environmental UI dependencies are stubbed.
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import ts from 'typescript';
export type Element = { type: unknown; key?: string; props: Record<string, any> };
export function componentHarness(file: string, mocks: Record<string, any>, sourceText?: string) {
 const slots:any[]=[];let cursor=0;const effects:(()=>void)[]=[];const cleanups=new Map<number,()=>void>();
 const memo=(factory:()=>any,deps?:any[])=>{const i=cursor++;const prior=slots[i];if(!prior||!deps||deps.length!==prior.deps?.length||deps.some((d,j)=>d!==prior.deps[j]))slots[i]={deps,value:factory()};return slots[i].value};
 const react={createContext:(initial:any)=>({Provider:'context-provider',value:initial}),useContext:(context:any)=>context.value,useState:(initial:any)=>{const i=cursor++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return [slots[i],(value:any)=>{slots[i]=typeof value==='function'?value(slots[i]):value}]},useRef:(initial:any)=>{const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i]},useCallback:(f:any,deps?:any[])=>memo(()=>f,deps),useMemo:memo,useEffect:(f:any,deps?:any[])=>{const i=cursor++;if(!slots[i]||!deps||deps.some((d,j)=>d!==slots[i][j])){slots[i]=deps;effects.push(()=>{cleanups.get(i)?.();const cleanup=f();if(typeof cleanup==='function')cleanups.set(i,cleanup);else cleanups.delete(i)})}}};
 const require=createRequire(file);const exports:any={};
 const source=ts.transpileModule(sourceText ?? readFileSync(file,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText;
 const sandboxGlobals=()=>({console,setTimeout,clearTimeout,URL,URLSearchParams,AbortController,TextEncoder,Error,crypto:globalThis.crypto});
 const tsxCache=new Map<string,{exports:any}>();
 // tsx 的 CJS 钩子不转译 .tsx（JSX 原文直灌 wrapSafe 即 SyntaxError）：包内组件互引 .tsx
 // 在此自行 transpile + 嵌套 vm 求值，require 复用 localRequire（递归），按绝对路径缓存保实例唯一。
 const loadTsx=(name:string)=>{const path=require.resolve(name);const hit=tsxCache.get(path);if(hit)return hit.exports;const out=ts.transpileModule(readFileSync(path,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText;const module_:{exports:any}={exports:{}};tsxCache.set(path,module_);vm.runInNewContext(out,{require:localRequire,exports:module_.exports,module:module_,...sandboxGlobals()},{filename:path});return module_.exports};
 function localRequire(name:string){if(name==='react')return react;if(name==='react/jsx-runtime')return {jsx:(type:any,props:any,key?:string)=>({type,props,key}),jsxs:(type:any,props:any,key?:string)=>({type,props,key})};if(name.endsWith('.css'))return {__esModule:true,default:new Proxy({},{get:(_,k)=>String(k)})};if(name in mocks)return mocks[name];if(name.endsWith('.tsx'))return loadTsx(name);return require(name)}
 vm.runInNewContext(source,{require:localRequire,exports,module:{exports},...sandboxGlobals(),...mocks.__globals},{filename:file});
 return {render:(name:string,props:any):Element=>{cursor=0;const tree=exports[name](props);while(effects.length)effects.shift()!();return tree},unmount:()=>{for(const cleanup of cleanups.values())cleanup();cleanups.clear()}};
}
export function elements(tree:any):Element[]{if(!tree||typeof tree!=='object')return [];if(Array.isArray(tree))return tree.flatMap(elements);return [tree,...elements(tree.props?.children)]}
