"use client";

// 修复轮 4(F2)最小守卫:门店段错误边界 —— 任何客户端异常不再整页白屏
// (「Application error」),给出诚实错误态与重试动作。正式的错误态/边界体系
// 归 HUI-2628 共享壳域;本文件只保 /work/stores 正常用户流程不再白屏。

export default function StoresError({ reset }: { error: Error; reset: () => void }) {
  return (
    <main>
      <h1 className="tk-title">门店</h1>
      <p className="tk-state tk-danger" data-state="error" data-field="stores-error">
        门店页没有打开。请稍等一下再重试；反复出现请联系平台。
        <button className="tk-quiet" type="button" data-action="retry-stores" onClick={() => reset()}>
          重试
        </button>
      </p>
    </main>
  );
}
