#import "CoreShim.h"
@import Nvp;

@interface NVCoreSink : NSObject <NvpSink>
@property(nonatomic, weak) NVCore *owner;
@end
@interface NVCore ()
@property(nonatomic, strong) NvpEngine *engine;
@property(nonatomic, copy) void (^packetBlock)(NSData *);
@property(nonatomic, copy) void (^failureBlock)(NSString *);
- (void)receive:(NSData *)packet;
- (void)failed:(NSString *)code;
@end
@implementation NVCore
- (instancetype)initWithPacket:(void (^)(NSData *))packet failure:(void (^)(NSString *))failure {
    if ((self = [super init])) {
        _packetBlock = [packet copy]; _failureBlock = [failure copy];
        NVCoreSink *sink = [NVCoreSink new]; sink.owner = self;
        _engine = NvpNewEngine(sink);
    }
    return self;
}
- (NSString *)begin:(NSString *)config error:(NSError **)error { return [_engine begin:config error:error]; }
- (BOOL)send:(NSData *)packet error:(NSError **)error { return [_engine send:packet error:error]; }
- (NSString *)stats { return [_engine statsJSON]; }
- (void)close { [_engine close]; }
- (void)receive:(NSData *)packet { if (packet) { self.packetBlock([packet copy]); } }
- (void)failed:(NSString *)code { self.failureBlock(code ?: @"nvp_failed"); }
+ (BOOL)verify:(NSData *)catalog keys:(NSData *)keys error:(NSError **)error { return NvpVerifyCatalog(catalog, keys, error); }
@end
@implementation NVCoreSink
- (void)receive:(NSData *)packet { [self.owner receive:packet]; }
- (void)failed:(NSString *)code { [self.owner failed:code]; }
@end
