/*
 * Copyright 2020 Google Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package xyz.aresius.tuttifrutti;

import android.annotation.SuppressLint;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.net.Uri;
import android.os.Bundle;

import androidx.annotation.Nullable;

/**
 * Opens the website in a Trusted Web Activity. This activity is exported (launcher and verified
 * App Links) so every URL it receives is untrusted: only what {@link LaunchUrlValidator}
 * accepts, rebuilt from constants, is ever launched; anything else opens the home page.
 */
public class LauncherActivity
        extends com.google.androidbrowserhelper.trusted.LauncherActivity {

    @Override
    @SuppressLint("SourceLockedOrientationActivity")
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // The game is portrait-only (web manifest "orientation": "portrait"): keep the splash
        // screen portrait too. The web content follows the SCREEN_ORIENTATION meta-data of the
        // manifest. (Bubblewrap only guards this call for API <= 26, below our minSdk.)
        setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_PORTRAIT);
    }

    /**
     * The library also forwards this URL to the browser as the "original launch URL", so it is
     * sanitised here too: an invalid or foreign URL behaves as if the intent carried no data.
     */
    @Override
    @Nullable
    protected Uri getUrlForIntent(Intent intent) {
        Uri data = intent.getData();
        String valid = LaunchUrlValidator.validate(data == null ? null : data.toString());
        return valid == null ? null : Uri.parse(valid);
    }

    @Override
    protected Uri getLaunchingUrl() {
        Uri url = super.getLaunchingUrl();
        return Uri.parse(LaunchUrlValidator.validateOrDefault(url == null ? null : url.toString()));
    }
}
