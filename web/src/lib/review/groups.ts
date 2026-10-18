/**
 * Which answers of a category "count as the same", mirroring `linkAnswers` in
 * `server/internal/game/scoring.go`: valid answers with the same normalized
 * text, plus the pairs of passed merge proposals (transitively). Only valid
 * answers take part.
 */
import type { ProposalView, RoundResult } from '$lib/protocol';

class Sets {
	#parent = new Map<string, string>();
	constructor(members: readonly string[]) {
		for (const id of members) this.#parent.set(id, id);
	}
	has(id: string): boolean {
		return this.#parent.has(id);
	}
	find(id: string): string {
		let root = id;
		while (this.#parent.get(root) !== root) root = this.#parent.get(root) as string;
		return root;
	}
	union(a: string, b: string): void {
		this.#parent.set(this.find(a), this.find(b));
	}
	members(id: string): string[] {
		const root = this.find(id);
		return [...this.#parent.keys()].filter((k) => this.find(k) === root);
	}
}

export interface AnswerGroups {
	/** Valid players of the category, in participant order. */
	valid: string[];
	/** Players whose answers count the same as `id`'s, `id` included (empty if `id` is not valid). */
	group(id: string): string[];
	/** Other players whose valid answer counts the same as `id`'s. */
	sharedWith(id: string): number;
	same(a: string, b: string): boolean;
}

export function answerGroups(
	participants: readonly string[],
	result: RoundResult | undefined,
	proposals: readonly ProposalView[],
	category: number
): AnswerGroups {
	const cellOf = (id: string) => result?.players[id]?.cells[category];
	const valid = participants.filter((id) => cellOf(id)?.status === 'valid');
	const sets = new Sets(valid);
	const firstWith = new Map<string, string>();
	for (const id of valid) {
		const answer = cellOf(id)?.answer ?? '';
		const first = firstWith.get(answer);
		if (first === undefined) firstWith.set(answer, id);
		else sets.union(first, id);
	}
	for (const pr of proposals) {
		if (pr.category !== category || !pr.passed) continue;
		if (sets.has(pr.playerA) && sets.has(pr.playerB)) sets.union(pr.playerA, pr.playerB);
	}
	return {
		valid,
		group: (id) => (sets.has(id) ? sets.members(id) : []),
		sharedWith: (id) => (sets.has(id) ? sets.members(id).length - 1 : 0),
		same: (a, b) => sets.has(a) && sets.has(b) && sets.find(a) === sets.find(b)
	};
}
