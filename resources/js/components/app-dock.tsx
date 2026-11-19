import { Link } from '@inertiajs/react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useCurrentUrl } from '@/hooks/use-current-url';
import { useNavigation } from '@/hooks/use-navigation';
import { cn } from '@/lib/utils';

/** Remembers, per browser, whether the viewer collapsed the dock. */
const DOCK_HIDDEN_KEY = 'rayxo:dock-hidden';

const PILL_SHADOW =
    'shadow-[0_12px_40px_rgb(36_77_139/0.16)] dark:shadow-[0_12px_40px_rgb(0_0_0/0.45)]';

/**
 * The floating quick-launch dock at the bottom of the reference design, with
 * the same pages as the sidebar for the account's role. It sticks to the
 * bottom of the content area (beside the sidebar, never over it), from
 * screens wide enough for it. Icon-only to stay compact; each icon keeps a
 * tooltip and an accessible label in place of the removed caption. A toggle
 * collapses it to a single pill the viewer can reopen, like the sidebar.
 */
export function AppDock() {
    const { dockItems } = useNavigation();
    const { isCurrentOrParentUrl } = useCurrentUrl();
    const [hidden, setHidden] = useState(false);

    useEffect(() => {
        try {
            setHidden(localStorage.getItem(DOCK_HIDDEN_KEY) === '1');
        } catch {
            // Storage can be unavailable (private mode); fall back to shown.
        }
    }, []);

    const setDockHidden = (next: boolean) => {
        setHidden(next);
        try {
            localStorage.setItem(DOCK_HIDDEN_KEY, next ? '1' : '0');
        } catch {
            // Ignore: the dock still toggles for this view without persistence.
        }
    };

    if (hidden) {
        return (
            <div className="pointer-events-none sticky bottom-3 z-30 hidden justify-center px-4 pb-3 lg:flex">
                <button
                    type="button"
                    aria-label="Tampilkan dock"
                    title="Tampilkan dock"
                    onClick={() => setDockHidden(false)}
                    className={cn(
                        'pointer-events-auto flex size-8 items-center justify-center rounded-full border bg-card/85 text-muted-foreground backdrop-blur-xl transition-colors hover:text-foreground',
                        PILL_SHADOW,
                    )}
                >
                    <ChevronUp className="size-4" strokeWidth={1.7} />
                </button>
            </div>
        );
    }

    return (
        <nav
            aria-label="Dock"
            className="pointer-events-none sticky bottom-3 z-30 hidden justify-center px-4 pb-3 lg:flex"
        >
            <ul
                className={cn(
                    'pointer-events-auto flex items-center gap-0.5 rounded-2xl border bg-card/85 px-1.5 py-1.5 backdrop-blur-xl',
                    PILL_SHADOW,
                )}
            >
                {dockItems.map((item) => {
                    const active = isCurrentOrParentUrl(item.href);

                    return (
                        <li key={item.title}>
                            <Link
                                href={item.href}
                                prefetch
                                title={item.title}
                                aria-label={item.title}
                                className={cn(
                                    'flex size-8 items-center justify-center rounded-lg transition-all hover:-translate-y-0.5',
                                    active
                                        ? 'rayxo-brand-gradient text-white shadow-brand'
                                        : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                                )}
                            >
                                {item.icon && (
                                    <item.icon
                                        className="size-[17px]"
                                        strokeWidth={1.7}
                                    />
                                )}
                            </Link>
                        </li>
                    );
                })}

                <li aria-hidden className="mx-0.5 h-5 w-px bg-border" />

                <li>
                    <button
                        type="button"
                        aria-label="Sembunyikan dock"
                        title="Sembunyikan dock"
                        onClick={() => setDockHidden(true)}
                        className="flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                    >
                        <ChevronDown className="size-4" strokeWidth={1.7} />
                    </button>
                </li>
            </ul>
        </nav>
    );
}
