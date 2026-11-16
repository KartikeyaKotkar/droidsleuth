#!/bin/sh
# Builds only local test artifacts. Does not install, launch or change a device.
set -eu
umask 077
sdk=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-"$HOME/Android/Sdk"}}
tools="$sdk/build-tools/36.0.0"
android="$sdk/platforms/android-36/android.jar"
output=${1:?usage: sh scripts/build-validation-apk.sh NEW_OUTPUT_DIRECTORY}
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir "$output"
mkdir "$output/classes" "$output/dex"
javac --release 8 -classpath "$android" -d "$output/classes" "$root/testdata/android/MainActivity.java"
"$tools/d8" --lib "$android" --min-api 30 --output "$output/dex" \
  "$output/classes/io/github/droidsleuth/validation/MainActivity.class"
"$tools/aapt2" link --manifest "$root/testdata/android/AndroidManifest.xml" \
  -I "$android" -o "$output/base.apk"
jar uf "$output/base.apk" -C "$output/dex" classes.dex
"$tools/zipalign" -f 4 "$output/base.apk" "$output/aligned.apk"
keytool -genkeypair -noprompt -keystore "$output/disposable.keystore" \
  -storepass android -keypass android -alias disposable -keyalg RSA -keysize 2048 \
  -validity 30 -dname 'CN=DroidSleuth Disposable Test'
"$tools/apksigner" sign --ks "$output/disposable.keystore" --ks-key-alias disposable \
  --ks-pass pass:android --key-pass pass:android --out "$output/disposable.apk" "$output/aligned.apk"
"$tools/apksigner" verify "$output/disposable.apk"
"$tools/aapt2" dump badging "$output/disposable.apk"
