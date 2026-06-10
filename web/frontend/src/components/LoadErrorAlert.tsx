import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function LoadErrorAlert({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <Alert variant="destructive">
      <AlertTitle className="font-mono uppercase tracking-[0.1em]">
        Error
      </AlertTitle>
      <AlertDescription>
        {message}
        <Button
          variant="ghost"
          size="sm"
          onClick={onRetry}
          className="ml-2 h-7 text-xs uppercase tracking-[0.1em]"
        >
          retry
        </Button>
      </AlertDescription>
    </Alert>
  );
}
