import { timestampMs, type Timestamp } from "@bufbuild/protobuf/wkt";

export { cn } from "cn";

export const initials = (name: string) =>
	(name.match(/[\p{L}\p{N}]+/gu) ?? [])
		.slice(0, 2)
		.map((w) => w[0])
		.join("")
		.toUpperCase();

// Relative age of a timestamp: "now", "5m", "2h", "3d".
export function ago(ts?: Timestamp, now = Date.now()) {
	const s = ts ? (now - timestampMs(ts)) / 1000 : 0;
	const [unit, size] = (
		[["d", 86400], ["h", 3600], ["m", 60]] as const
	).find(([, n]) => s >= n) ?? ["", 0];
	return unit ? Math.floor(s / size) + unit : "now";
}

/** Dollars to the cent; below a cent, the digits that say how little. */
export const usd = (n: number) =>
	new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD', maximumFractionDigits: n > 0 && n < 0.01 ? 4 : 2 }).format(n);

/** ago as words: "just now", "2h ago". */
export function since(ts?: Timestamp) {
	const a = ago(ts);
	return a === "now" ? "just now" : `${a} ago`;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChild<T> = T extends { child?: any } ? Omit<T, "child"> : T;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChildren<T> = T extends { children?: any } ? Omit<T, "children"> : T;
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>;
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & { ref?: U | null };
