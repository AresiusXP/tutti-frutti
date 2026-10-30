// See https://svelte.dev/docs/kit/types#app.d.ts
declare global {
	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
		interface PageState {
			/** Modal sheets open, one history entry each (`$lib/lobby/sheetBack.ts`). */
			tfSheet?: number;
		}
		// interface Platform {}
	}
}

export {};
