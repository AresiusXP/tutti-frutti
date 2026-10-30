import { vi } from 'vitest';

/** Just enough of the service worker API for the update flow (`update.svelte.ts`). */
export class FakeWorker extends EventTarget {
	state: ServiceWorkerState;
	postMessage = vi.fn();
	constructor(state: ServiceWorkerState) {
		super();
		this.state = state;
	}
	moveTo(state: ServiceWorkerState) {
		this.state = state;
		this.dispatchEvent(new Event('statechange'));
	}
}

export class FakeRegistration {
	waiting: FakeWorker | null = null;
	installing: FakeWorker | null = null;
	update = vi.fn(() => Promise.resolve());
}

export class FakeContainer extends EventTarget {
	controller: object | null = {};
	registration: FakeRegistration | undefined = new FakeRegistration();
	getRegistration = vi.fn(() => Promise.resolve(this.registration));
}

/** Puts a fake `navigator.serviceWorker` and `location.reload` in place; `restoreFakeServiceWorker` undoes it. */
export function installFakeServiceWorker() {
	const container = new FakeContainer();
	Object.defineProperty(navigator, 'serviceWorker', { value: container, configurable: true });
	const reload = vi.fn();
	vi.stubGlobal('location', { reload });
	return { container, reload };
}

export function restoreFakeServiceWorker() {
	Reflect.deleteProperty(navigator, 'serviceWorker');
	Reflect.deleteProperty(document, 'visibilityState');
	vi.unstubAllGlobals();
}
