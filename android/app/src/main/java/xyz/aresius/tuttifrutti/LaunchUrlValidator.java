package xyz.aresius.tuttifrutti;

import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/**
 * Decides which URLs the exported {@link LauncherActivity} may open. Pure Java (no Android
 * classes), unit-tested in LaunchUrlValidatorTest.
 *
 * <p>An URL is accepted only if ALL of the following hold:
 * <ul>
 *   <li>printable ASCII only (no control characters, whitespace, Unicode look-alikes),
 *   <li>scheme {@code https},
 *   <li>authority exactly {@link #HOST}: no userinfo, no port other than the default 443, no
 *       trailing dot, no subdomain,
 *   <li>path is one of {@link #STATIC_PATHS} (exact, case-sensitive, no trailing slash) or a room
 *       invite: {@code /} plus exactly {@link #ROOM_CODE_LENGTH} characters of
 *       {@link #ROOM_CODE_ALPHABET} (any ASCII case, like web/src/params/roomcode.ts).
 * </ul>
 *
 * <p>The result is never the input: it is rebuilt from constants as
 * {@code https://HOST/<path>}. The query and the fragment are dropped (the TWA need not forward
 * them and the web app does not depend on them to open a room). Room codes are upper-cased with
 * ASCII rules only, like {@code asciiUpper} in web/src/lib/roomCode.ts.
 *
 * <p>Keep in sync with: the intent-filter in AndroidManifest.xml (a test enforces it), the room
 * code alphabet in server/internal/room/hub.go and web/src/lib/roomCode.ts, and the web routes.
 */
public final class LaunchUrlValidator {

    /** The only origin the app opens (single source of truth: app/build.gradle). */
    public static final String HOST = BuildConfig.HOST_NAME;

    private static final String ORIGIN = "https://" + HOST;

    /** Where the app opens when there is no (valid) URL. */
    public static final String DEFAULT_URL = ORIGIN + "/";

    /** Pages of the website the app may be opened on; never /api/*, /ws or the OAuth callback. */
    static final List<String> STATIC_PATHS = Collections.unmodifiableList(Arrays.asList(
            "/", "/rooms", "/stats", "/privacy", "/terms", "/delete-account"));

    /** Same alphabet as the server: no vowels (no words) and no look-alikes (0/1/O/I). */
    static final String ROOM_CODE_ALPHABET = "BCDFGHJKLMNPQRSTVWXZ23456789";

    static final int ROOM_CODE_LENGTH = 4;

    /** The manifest intent-filter pathPattern of a room invite: "/" plus any 4 characters. */
    static final String ROOM_PATH_PATTERN = "/....";

    private static final String SCHEME_PREFIX = "https://";
    private static final String DEFAULT_PORT_SUFFIX = ":443";
    private static final int MAX_URL_LENGTH = 2048;

    private LaunchUrlValidator() {
    }

    /**
     * Returns the canonical URL to open for {@code url}, or {@code null} when it must not be
     * opened.
     */
    public static String validate(String url) {
        if (url == null || url.isEmpty() || url.length() > MAX_URL_LENGTH || !isPrintableAscii(url)) {
            return null;
        }
        if (!startsWithIgnoreAsciiCase(url, SCHEME_PREFIX)) {
            return null;
        }

        String rest = url.substring(SCHEME_PREFIX.length());
        int authorityEnd = indexOfAny(rest, "/?#");
        String authority = authorityEnd < 0 ? rest : rest.substring(0, authorityEnd);
        if (!authority.equalsIgnoreCase(HOST)
                && !authority.equalsIgnoreCase(HOST + DEFAULT_PORT_SUFFIX)) {
            return null;
        }

        String afterAuthority = authorityEnd < 0 ? "" : rest.substring(authorityEnd);
        int pathEnd = indexOfAny(afterAuthority, "?#");
        String path = pathEnd < 0 ? afterAuthority : afterAuthority.substring(0, pathEnd);
        if (path.isEmpty()) {
            path = "/";
        }

        if (STATIC_PATHS.contains(path)) {
            return ORIGIN + path;
        }
        String code = roomCodeOf(path);
        return code == null ? null : ORIGIN + "/" + code;
    }

    /** {@link #validate} with {@link #DEFAULT_URL} as the answer for anything invalid. */
    public static String validateOrDefault(String url) {
        String valid = validate(url);
        return valid != null ? valid : DEFAULT_URL;
    }

    /** The upper-cased code of {@code /XXXX}, or {@code null} when {@code path} is not an invite. */
    private static String roomCodeOf(String path) {
        if (path.length() != 1 + ROOM_CODE_LENGTH || path.charAt(0) != '/') {
            return null;
        }
        StringBuilder code = new StringBuilder(ROOM_CODE_LENGTH);
        for (int i = 1; i < path.length(); i++) {
            char c = asciiUpper(path.charAt(i));
            if (ROOM_CODE_ALPHABET.indexOf(c) < 0) {
                return null;
            }
            code.append(c);
        }
        return code.toString();
    }

    private static char asciiUpper(char c) {
        return c >= 'a' && c <= 'z' ? (char) (c - 'a' + 'A') : c;
    }

    private static boolean isPrintableAscii(String s) {
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (c < 0x21 || c > 0x7E) {
                return false;
            }
        }
        return true;
    }

    /** Only called on printable ASCII, where equalsIgnoreCase has no Unicode case folding. */
    private static boolean startsWithIgnoreAsciiCase(String s, String prefix) {
        return s.length() >= prefix.length()
                && s.regionMatches(true, 0, prefix, 0, prefix.length());
    }

    private static int indexOfAny(String s, String chars) {
        for (int i = 0; i < s.length(); i++) {
            if (chars.indexOf(s.charAt(i)) >= 0) {
                return i;
            }
        }
        return -1;
    }
}
