import { useNavigate } from "react-router-dom";
import { ExternalLink, MapPin } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

// A lat,lon link that opens a map-target menu (MeshCore map / Google / Apple).
// `raw` is the visible label; `className` merges onto the trigger.
export function CoordLink({
  lat,
  lon,
  raw,
  className,
}: {
  lat: number;
  lon: number;
  raw: string;
  className?: string;
}) {
  const navigate = useNavigate();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          className={cn(
            "font-medium text-info underline decoration-dotted underline-offset-2 hover:text-info/80",
            className,
          )}
        >
          {raw}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        onClick={(e) => e.stopPropagation()}
        className="rounded-none font-mono text-xs"
      >
        <DropdownMenuItem onClick={() => navigate(`/map?lat=${lat}&lon=${lon}`)}>
          <MapPin className="size-3.5" /> Open in MeshCore Map
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() =>
            window.open(
              `https://www.google.com/maps/search/?api=1&query=${lat},${lon}`,
              "_blank",
              "noopener,noreferrer",
            )
          }
        >
          <ExternalLink className="size-3.5" /> Open in Google Maps
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() =>
            window.open(
              `https://maps.apple.com/?ll=${lat},${lon}&q=${lat},${lon}`,
              "_blank",
              "noopener,noreferrer",
            )
          }
        >
          <ExternalLink className="size-3.5" /> Open in Apple Maps
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
