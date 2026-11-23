#!/bin/sh
# Build four disposable APKs locally. ADB is not used.
set -eu
umask 077
sdk=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-"$HOME/Android/Sdk"}}
tools="$sdk/build-tools/36.0.0"
android="$sdk/platforms/android-36/android.jar"
output=${1:?usage: sh testdata/android/private-workers/build.sh NEW_OUTPUT_DIRECTORY}
src=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
package=io.github.droidsleuth.v03
mkdir "$output"
mkdir "$output/classes" "$output/dex"

javac --release 8 -classpath "$android" -d "$output/classes" "$src"/*.java
"$tools/d8" --lib "$android" --min-api 30 --output "$output/dex" \
    "$output"/classes/io/github/droidsleuth/v03/*.class
keytool -genkeypair -noprompt -keystore "$output/disposable.keystore" \
    -storepass android -keypass android -alias disposable -keyalg RSA \
    -keysize 2048 -validity 30 -dname 'CN=DroidSleuth v0.3 Disposable Test'

for role in main other shareda sharedb; do
    pkg=$package
    shared=''
    if [ "$role" != main ]; then pkg="$package.$role"; fi
    case "$role" in
        shared*) shared="android:sharedUserId=\"$package.shared\"";;
    esac
    manifest="$output/$role.xml"
    cat > "$manifest" <<EOF
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="$pkg" $shared>
    <uses-sdk android:minSdkVersion="30" android:targetSdkVersion="32" />
    <application android:allowBackup="false" android:debuggable="true"
        android:label="DroidSleuth disposable">
        <activity android:name="$package.FixtureActivity" android:exported="true" />
EOF
    if [ "$role" = main ]; then
        cat >> "$manifest" <<EOF
        <service android:name="$package.Remote" android:process=":remote" android:exported="true" />
        <service android:name="$package.ServiceWorker" android:process=":service" android:exported="true" />
        <service android:name="$package.Global" android:process="$package.global" android:exported="true" />
        <service android:name="$package.Isolated" android:process=":isolated"
            android:isolatedProcess="true" android:exported="false" />
EOF
    fi
    printf '    </application>\n</manifest>\n' >> "$manifest"
    "$tools/aapt2" link --manifest "$manifest" -I "$android" -o "$output/base.apk"
    jar uf "$output/base.apk" -C "$output/dex" classes.dex
    "$tools/zipalign" -f 4 "$output/base.apk" "$output/aligned.apk"
    "$tools/apksigner" sign --ks "$output/disposable.keystore" --ks-key-alias disposable \
        --ks-pass pass:android --key-pass pass:android --out "$output/$role.apk" "$output/aligned.apk"
    "$tools/apksigner" verify "$output/$role.apk"
done
