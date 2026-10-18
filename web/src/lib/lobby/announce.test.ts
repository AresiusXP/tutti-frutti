import { describe, expect, it } from 'vitest';
import { diffPlayers } from './announce';

const ana = { id: 'p1', name: 'Ana' };
const beto = { id: 'p2', name: 'Beto' };
const caro = { id: 'p3', name: 'Caro' };

describe('diffPlayers', () => {
	it('reports who joined and who left', () => {
		expect(diffPlayers([ana, beto], [ana, beto, caro])).toEqual({ joined: ['Caro'], left: [] });
		expect(diffPlayers([ana, beto, caro], [ana])).toEqual({ joined: [], left: ['Beto', 'Caro'] });
		expect(diffPlayers([ana, beto], [ana, caro])).toEqual({ joined: ['Caro'], left: ['Beto'] });
	});

	it('reports nothing when the seats are the same, whatever the order', () => {
		expect(diffPlayers([ana, beto], [beto, ana])).toEqual({ joined: [], left: [] });
		expect(diffPlayers([], [])).toEqual({ joined: [], left: [] });
	});
});
