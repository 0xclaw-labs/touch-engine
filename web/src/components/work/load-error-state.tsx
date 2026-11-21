"use client";

// One shared error-state row for work surfaces. The retry control lives in a
// component instead of a bare button in each route file, so the error shape is
// defined once (HUI-2628 finish anchors: campaigns, materials). The rendered
// data attributes and the label are unchanged from the inline markup this
// replaces; presentation now uses the shadcn Alert + Button primitives.
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function LoadErrorState({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <Alert
      variant="destructive"
      role="alert"
      data-state="error"
      className="tk-gap"
    >
      <AlertTitle>加载失败</AlertTitle>
      <AlertDescription className="tk-row">
        <span>{message}</span>
        <Button
          variant="outline"
          size="sm"
          type="button"
          data-action="retry"
          onClick={onRetry}
        >
          重试
        </Button>
      </AlertDescription>
    </Alert>
  );
}
