"use client";

// One shared error-state row for work surfaces. The retry control lives in a
// component instead of a bare button in each route file, so the error shape is
// defined once (HUI-2628 finish anchors: campaigns, materials). The rendered
// classes, the data attributes, and the label are unchanged from the inline
// markup this replaces.
export function LoadErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <p className="tk-state tk-danger" data-state="error">
      {message}
      <button className="tk-quiet" type="button" data-action="retry" onClick={onRetry}>
        重试
      </button>
    </p>
  );
}
