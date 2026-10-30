package xyz.aresius.tuttifrutti;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.io.File;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import javax.xml.parsers.DocumentBuilderFactory;

import org.junit.Test;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.NodeList;

public class LaunchUrlValidatorTest {

    private static final String ORIGIN = "https://tuttifrutti.aresius.xyz";
    private static final String ROOT = ORIGIN + "/";

    private static void assertAccepted(String expected, String input) {
        assertEquals(input, expected, LaunchUrlValidator.validate(input));
        assertEquals(input, expected, LaunchUrlValidator.validateOrDefault(input));
    }

    private static void assertRejected(String input) {
        assertNull("must be rejected: " + input, LaunchUrlValidator.validate(input));
        assertEquals("must fall back to the home page: " + input, ROOT,
                LaunchUrlValidator.validateOrDefault(input));
    }

    @Test
    public void hostAndDefaultUrlAreTheWebsite() {
        assertEquals("tuttifrutti.aresius.xyz", LaunchUrlValidator.HOST);
        assertEquals(ROOT, LaunchUrlValidator.DEFAULT_URL);
    }

    @Test
    public void acceptsRoot() {
        assertAccepted(ROOT, ORIGIN + "/");
        assertAccepted(ROOT, ORIGIN);
    }

    @Test
    public void acceptsEveryStaticPage() {
        for (String page : Arrays.asList("/rooms", "/stats", "/privacy", "/terms", "/delete-account")) {
            assertAccepted(ORIGIN + page, ORIGIN + page);
        }
    }

    @Test
    public void acceptsRoomInvites() {
        assertAccepted(ORIGIN + "/KT7P", ORIGIN + "/KT7P");
        assertAccepted(ORIGIN + "/BCDF", ORIGIN + "/BCDF");
        assertAccepted(ORIGIN + "/2345", ORIGIN + "/2345");
        assertAccepted(ORIGIN + "/XZ89", ORIGIN + "/XZ89");
    }

    @Test
    public void everyCharacterOfTheServerAlphabetIsAccepted() {
        String alphabet = "BCDFGHJKLMNPQRSTVWXZ23456789";
        for (char c : alphabet.toCharArray()) {
            assertAccepted(ORIGIN + "/" + c + c + c + c, ORIGIN + "/" + c + c + c + c);
        }
        assertEquals(alphabet, LaunchUrlValidator.ROOM_CODE_ALPHABET);
        assertEquals(28, alphabet.length());
    }

    @Test
    public void lowercaseRoomCodesAreAcceptedAndCanonicalised() {
        assertAccepted(ORIGIN + "/KT7P", ORIGIN + "/kt7p");
        assertAccepted(ORIGIN + "/KT7P", ORIGIN + "/Kt7p");
        assertAccepted(ORIGIN + "/BCDF", ORIGIN + "/bcdf");
    }

    @Test
    public void rejectsRoomCodesOutsideTheAlphabet() {
        // vowels, look-alikes (0, 1, O, I) and punctuation are not room-code characters
        for (String code : Arrays.asList("AEIO", "KT7A", "KT70", "KT71", "KTO7", "KTI7", "kt7a",
                "KT7-", "KT7_", "KT7.", "KT7%", "KT7 ", "KT\u00DF7", "KT7\u00C9")) {
            assertRejected(ORIGIN + "/" + code);
        }
    }

    @Test
    public void rejectsRoomCodesOfTheWrongLength() {
        assertRejected(ORIGIN + "/KT7");
        assertRejected(ORIGIN + "/KT7PX");
        assertRejected(ORIGIN + "/K");
        assertRejected(ORIGIN + "/KT7P7");
    }

    @Test
    public void rejectsApiWebSocketAndOauthCallback() {
        assertRejected(ORIGIN + "/api");
        assertRejected(ORIGIN + "/api/");
        assertRejected(ORIGIN + "/api/rooms");
        assertRejected(ORIGIN + "/api/session");
        assertRejected(ORIGIN + "/api/auth/google/callback");
        assertRejected(ORIGIN + "/api/auth/google/callback?code=abc&state=xyz");
        assertRejected(ORIGIN + "/api/auth/google/login?returnTo=/");
        assertRejected(ORIGIN + "/ws");
        assertRejected(ORIGIN + "/ws/");
        assertRejected(ORIGIN + "/healthz");
        assertRejected(ORIGIN + "/metrics");
        assertRejected(ORIGIN + "/.well-known/assetlinks.json");
        assertRejected(ORIGIN + "/dev");
        // 4 characters outside the alphabet: matched by the manifest glob, refused here
        assertRejected(ORIGIN + "/docs");
        assertRejected(ORIGIN + "/home");
    }

    @Test
    public void rejectsOtherSchemes() {
        assertRejected("http://tuttifrutti.aresius.xyz/");
        assertRejected("http://tuttifrutti.aresius.xyz/KT7P");
        assertRejected("ftp://tuttifrutti.aresius.xyz/");
        assertRejected("javascript:alert(1)");
        assertRejected("intent://tuttifrutti.aresius.xyz/#Intent;scheme=https;end");
        assertRejected("content://tuttifrutti.aresius.xyz/");
        assertRejected("file:///data/data/xyz.aresius.tuttifrutti/files/x");
        assertRejected("tuttifrutti.aresius.xyz/");
        assertRejected("//tuttifrutti.aresius.xyz/");
        assertRejected("https:tuttifrutti.aresius.xyz/");
        assertRejected("https:/tuttifrutti.aresius.xyz/");
        assertRejected("https:///tuttifrutti.aresius.xyz/");
        assertRejected("https:\\\\tuttifrutti.aresius.xyz\\");
    }

    @Test
    public void rejectsOtherHosts() {
        assertRejected("https://example.com/");
        assertRejected("https://aresius.xyz/");
        assertRejected("https://www.tuttifrutti.aresius.xyz/");
        assertRejected("https://api.tuttifrutti.aresius.xyz/");
        assertRejected("https://xtuttifrutti.aresius.xyz/");
        assertRejected("https://tuttifrutti.aresius.xyzx/");
        assertRejected("https://tuttifrutti.aresius.xyz./");
        assertRejected("https://tuttifrutti-aresius.xyz/");
        assertRejected("https://localhost/");
        assertRejected("https://127.0.0.1/");
        assertRejected("https:///");
        assertRejected("https://");
        assertRejected("https:// tuttifrutti.aresius.xyz/");
    }

    @Test
    public void rejectsUserinfoPortsAndLookalikeHosts() {
        assertRejected("https://user@tuttifrutti.aresius.xyz/");
        assertRejected("https://user:pass@tuttifrutti.aresius.xyz/");
        assertRejected("https://tuttifrutti.aresius.xyz@evil.com/");
        assertRejected("https://tuttifrutti.aresius.xyz:pass@evil.com/");
        assertRejected("https://evil.com@tuttifrutti.aresius.xyz/");
        assertRejected("https://tuttifrutti.aresius.xyz:8443/");
        assertRejected("https://tuttifrutti.aresius.xyz:80/");
        assertRejected("https://tuttifrutti.aresius.xyz:/");
        assertRejected("https://tuttifrutti.aresius.xyz:0443/");
        assertRejected("https://tuttifrutti.aresius.xyz.evil.com/");
        assertRejected("https://tuttifrutti.aresius.xyz.evil.com/KT7P");
        assertRejected("https://evil.com/tuttifrutti.aresius.xyz/");
        assertRejected("https://evil.com/?https://tuttifrutti.aresius.xyz/");
        assertRejected("https://evil.com#@tuttifrutti.aresius.xyz/");
        assertRejected("https://tuttifrutti.aresius.xyz\\@evil.com/");
        assertRejected("https://evil.com\\.tuttifrutti.aresius.xyz/");
        // Unicode look-alikes (long s, Kelvin sign, Cyrillic a, fullwidth letters)
        assertRejected("https://tuttifrutti.are\u017Fius.xyz/");
        assertRejected("https://tuttifrutti.\u0430resius.xyz/");
        assertRejected("https://\uFF54uttifrutti.aresius.xyz/");
        assertRejected("ht\u017Fp\u017F://tuttifrutti.aresius.xyz/");
        assertRejected("https://tuttifrutti.aresius.xyz/KT7\u0420");
    }

    @Test
    public void defaultPortIsAcceptedAndDropped() {
        assertAccepted(ROOT, "https://tuttifrutti.aresius.xyz:443/");
        assertAccepted(ORIGIN + "/KT7P", "https://tuttifrutti.aresius.xyz:443/KT7P");
    }

    @Test
    public void schemeAndHostAreCaseInsensitiveAndRebuiltFromConstants() {
        assertAccepted(ROOT, "HTTPS://TUTTIFRUTTI.ARESIUS.XYZ/");
        assertAccepted(ORIGIN + "/stats", "Https://TuttiFrutti.Aresius.Xyz/stats");
    }

    @Test
    public void rejectsNullEmptyAndBlank() {
        assertRejected(null);
        assertRejected("");
        assertRejected(" ");
        assertRejected("\t");
        assertRejected("\n");
        assertRejected(ROOT + " ");
        assertRejected(" " + ROOT);
        assertRejected(ROOT + "\n");
        assertRejected(ORIGIN + "/ro\tms");
        assertRejected(ORIGIN + "/rooms\u0000");
        assertRejected(ORIGIN + "/\u0000KT7P");
    }

    @Test
    public void rejectsOverlongUrls() {
        StringBuilder query = new StringBuilder("?q=");
        for (int i = 0; i < 3000; i++) {
            query.append('a');
        }
        assertRejected(ROOT + query);
    }

    @Test
    public void rejectsPathTraversal() {
        assertRejected(ORIGIN + "/..");
        assertRejected(ORIGIN + "/../");
        assertRejected(ORIGIN + "/../api/rooms");
        assertRejected(ORIGIN + "/rooms/../api/session");
        assertRejected(ORIGIN + "/rooms/..");
        assertRejected(ORIGIN + "/./rooms");
        assertRejected(ORIGIN + "/rooms/.");
        assertRejected(ORIGIN + "//rooms");
        assertRejected(ORIGIN + "//");
        assertRejected(ORIGIN + "/KT7P/..");
        assertRejected(ORIGIN + "/KT7P/../api");
        assertRejected(ORIGIN + "/..;/api");
        assertRejected(ORIGIN + "/rooms;x=1");
        assertRejected(ORIGIN + "/rooms\\..\\api");
        assertRejected(ORIGIN + "/api/../rooms");
        assertRejected(ORIGIN + "/%2e%2e/api");
        assertRejected(ORIGIN + "/.%2e/api");
        assertRejected(ORIGIN + "/%2E%2E%2Fapi");
    }

    @Test
    public void rejectsEncodedCharactersInThePath() {
        assertRejected(ORIGIN + "/rooms%2F..%2Fapi");
        assertRejected(ORIGIN + "/%2Fapi");
        assertRejected(ORIGIN + "/%2Frooms");
        assertRejected(ORIGIN + "/rooms%2f");
        assertRejected(ORIGIN + "/api%2Fsession");
        assertRejected(ORIGIN + "/%72ooms");
        assertRejected(ORIGIN + "/ro%6Fms");
        assertRejected(ORIGIN + "/%4BT7P");
        assertRejected(ORIGIN + "/KT7%50");
        assertRejected(ORIGIN + "/%00");
        assertRejected(ORIGIN + "/delete%2Daccount");
    }

    @Test
    public void rejectsTrailingSlashAndOtherPathVariants() {
        assertRejected(ORIGIN + "/rooms/");
        assertRejected(ORIGIN + "/stats/");
        assertRejected(ORIGIN + "/privacy/");
        assertRejected(ORIGIN + "/terms/");
        assertRejected(ORIGIN + "/delete-account/");
        assertRejected(ORIGIN + "/KT7P/");
        assertRejected(ORIGIN + "/kt7p/");
        assertRejected(ORIGIN + "//KT7P");
        assertRejected(ORIGIN + "/Rooms");
        assertRejected(ORIGIN + "/STATS");
        assertRejected(ORIGIN + "/privacy.html");
        assertRejected(ORIGIN + "/index.html");
        assertRejected(ORIGIN + "/rooms.html");
        assertRejected(ORIGIN + "/delete_account");
        assertRejected(ORIGIN + "/delete-account/confirm");
        assertRejected(ORIGIN + "/rooms/KT7P");
    }

    @Test
    public void onlyTheValidatedPathIsForwarded() {
        assertAccepted(ROOT, ORIGIN + "/?utm_source=x");
        assertAccepted(ROOT, ORIGIN + "/#fragment");
        assertAccepted(ORIGIN + "/KT7P", ORIGIN + "/KT7P?ref=chat#top");
        assertAccepted(ORIGIN + "/KT7P", ORIGIN + "/kt7p?x=%2e%2e#y");
        assertAccepted(ORIGIN + "/stats", ORIGIN + "/stats?next=https://evil.com/");
        assertAccepted(ORIGIN + "/privacy", ORIGIN + "/privacy#es");
        assertAccepted(ROOT, ORIGIN + "?x=1");
        // an invalid path stays invalid whatever the query says
        assertRejected(ORIGIN + "/api?next=/rooms");
        assertRejected(ORIGIN + "/ws#/rooms");
        assertRejected(ORIGIN + "/rooms/?x=1");
    }

    @Test
    public void validateOrDefaultNeverReturnsAnythingElse() {
        List<String> inputs = Arrays.asList(null, "", ROOT, ORIGIN + "/kt7p?x=1", "http://evil.com/",
                ORIGIN + "/api/auth/google/callback?code=1", "https://evil.com/KT7P");
        for (String input : inputs) {
            String out = LaunchUrlValidator.validateOrDefault(input);
            assertTrue(out, out.equals(ROOT) || out.matches("https://tuttifrutti\\.aresius\\.xyz/"
                    + "(rooms|stats|privacy|terms|delete-account|[BCDFGHJKLMNPQRSTVWXZ2-9]{4})"));
        }
    }

    @Test
    public void roomPathPatternIsASlashAndFourAnyCharacters() {
        StringBuilder expected = new StringBuilder("/");
        for (int i = 0; i < LaunchUrlValidator.ROOM_CODE_LENGTH; i++) {
            expected.append('.');
        }
        assertEquals(expected.toString(), LaunchUrlValidator.ROOM_PATH_PATTERN);
    }

    /**
     * The manifest intent-filter and the validator must describe the same set of pages. Gradle
     * runs unit tests with the module directory (app/) as the working directory.
     */
    @Test
    public void manifestMatchesValidator() throws Exception {
        File manifest = new File("src/main/AndroidManifest.xml");
        assertTrue("manifest not found at " + manifest.getAbsolutePath(), manifest.isFile());
        DocumentBuilderFactory factory = DocumentBuilderFactory.newInstance();
        factory.setNamespaceAware(true);
        Document doc = factory.newDocumentBuilder().parse(manifest);
        String androidNs = "http://schemas.android.com/apk/res/android";

        List<String> paths = new ArrayList<>();
        List<String> patterns = new ArrayList<>();
        Element verified = null;
        NodeList filters = doc.getElementsByTagName("intent-filter");
        for (int i = 0; i < filters.getLength(); i++) {
            Element filter = (Element) filters.item(i);
            if ("true".equals(filter.getAttributeNS(androidNs, "autoVerify"))) {
                assertNull("exactly one verified intent-filter expected", verified);
                verified = filter;
            }
        }
        assertNotNull("verified App Links intent-filter missing", verified);

        NodeList data = verified.getElementsByTagName("data");
        List<String> schemes = new ArrayList<>();
        List<String> hosts = new ArrayList<>();
        for (int i = 0; i < data.getLength(); i++) {
            Element d = (Element) data.item(i);
            assertFalse("no path prefix/suffix/advanced pattern allowed",
                    d.hasAttributeNS(androidNs, "pathPrefix")
                            || d.hasAttributeNS(androidNs, "pathSuffix")
                            || d.hasAttributeNS(androidNs, "pathAdvancedPattern"));
            if (d.hasAttributeNS(androidNs, "scheme")) {
                schemes.add(d.getAttributeNS(androidNs, "scheme"));
            }
            if (d.hasAttributeNS(androidNs, "host")) {
                hosts.add(d.getAttributeNS(androidNs, "host"));
            }
            if (d.hasAttributeNS(androidNs, "path")) {
                paths.add(d.getAttributeNS(androidNs, "path"));
            }
            if (d.hasAttributeNS(androidNs, "pathPattern")) {
                patterns.add(d.getAttributeNS(androidNs, "pathPattern"));
            }
        }
        assertEquals(Arrays.asList("https"), schemes);
        assertEquals(Arrays.asList("${hostName}"), hosts);
        assertEquals(LaunchUrlValidator.STATIC_PATHS, paths);
        assertEquals(Arrays.asList(LaunchUrlValidator.ROOM_PATH_PATTERN), patterns);
    }
}
