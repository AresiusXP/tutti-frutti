import type { ParamMatcher } from '@sveltejs/kit';

/** `/KT7P` (any case): 4 characters of the room-code alphabet BCDFGHJKLMNPQRSTVWXZ23456789. */
export const match: ParamMatcher = (param) => /^[bcdfghjklmnpqrstvwxz2-9]{4}$/i.test(param);
