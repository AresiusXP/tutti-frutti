export interface Toast {
	id: number;
	text: string;
}

const DURATION_MS = 2800;
let nextId = 1;

/** Tiny toast queue (one message visible at a time is enough for now). */
class ToastStore {
	items = $state<Toast[]>([]);

	show(text: string) {
		const id = nextId++;
		this.items = [...this.items, { id, text }].slice(-3);
		setTimeout(() => this.dismiss(id), DURATION_MS);
	}

	dismiss(id: number) {
		this.items = this.items.filter((t) => t.id !== id);
	}
}

export const toasts = new ToastStore();
