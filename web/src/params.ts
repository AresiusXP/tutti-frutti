import { defineParams } from '@sveltejs/kit/params';

/** `/KT7P` (any case): 4 characters of the room-code alphabet BCDFGHJKLMNPQRSTVWXZ23456789. */
const matchRoomcode = (param: string) => /^[bcdfghjklmnpqrstvwxz2-9]{4}$/i.test(param);

export const params = defineParams({
	roomcode: (param) => (matchRoomcode(param) ? param : undefined)
});
