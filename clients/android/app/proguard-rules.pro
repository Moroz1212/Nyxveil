# Keep JNI / bridge / crypto reflection surfaces.
-keep class ru.nyxveil.android.bridge.** { *; }
-keep class ru.nyxveil.bridge.** { *; }
-keep class go.** { *; }
-keep class nyxveilbridge.** { *; }
-dontwarn okhttp3.**
-dontwarn okio.**
-dontwarn com.google.errorprone.annotations.**
-dontwarn javax.annotation.**
-dontwarn org.codehaus.mojo.animal_sniffer.**
