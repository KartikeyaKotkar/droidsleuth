package io.github.droidsleuth.v03;

import android.app.Service;
import android.content.Intent;
import android.os.*;

public class Remote extends Service {
    @Override public IBinder onBind(Intent intent) { return new Binder(); }

    @Override public int onStartCommand(Intent intent, int flags, int id) {
        String mode = intent == null ? null : intent.getStringExtra("mode");
        if ("exit".equals(mode)) {
            new Handler(getMainLooper()).postDelayed(() -> {
                stopSelf();
                System.exit(0);
            }, 300);
        } else if ("crash".equals(mode)) {
            new Handler(getMainLooper()).postDelayed(() -> {
                throw new IllegalStateException("Disposable v0.3 worker crash");
            }, 300);
        }
        return START_NOT_STICKY;
    }
}
