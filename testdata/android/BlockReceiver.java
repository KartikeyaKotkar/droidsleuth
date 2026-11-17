package io.github.droidsleuth.validation;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.SystemClock;

/** Deliberately blocks only this disposable app's main thread for ANR validation. */
public final class BlockReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        SystemClock.sleep(120000);
    }
}
